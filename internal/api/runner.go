package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RunState represents the current state of the research runner
type RunState string

const (
	StateIdle    RunState = "idle"
	StateRunning RunState = "running"
	StatePaused  RunState = "paused"
	StateStopping RunState = "stopping"
)

// RunMode represents the mode of operation
type RunMode string

const (
	ModeFull           RunMode = "full"
	ModeBackfillImages RunMode = "backfill-images"
	ModeGenerateImages RunMode = "generate-images"
)

// ResearchRunner manages research run execution with start/stop/pause support
type ResearchRunner struct {
	mu           sync.RWMutex
	state        RunState
	mode         RunMode
	currentStep  string
	progress     int // 0-100
	startTime    *time.Time
	pid          int
	
	// Control channels
	pauseCh      chan struct{}
	resumeCh     chan struct{}
	stopCh       chan struct{}
	
	// Process management
	cmd          *exec.Cmd
	cancelFunc   context.CancelFunc
	
	repoPath     string
	broadcastFn  func(StatusUpdate)
}

type runnerPIDFile struct {
	PID       int     `json:"pid"`
	Mode      RunMode `json:"mode"`
	StartedAt string  `json:"startedAt"`
}

func (r *ResearchRunner) pidFilePath() string {
	return filepath.Join(r.repoPath, "Compendium", "_debug", "dashboard_runner_pid.json")
}

func (r *ResearchRunner) writePIDFile(pid int, mode RunMode) {
	if r.repoPath == "" {
		return
	}
	path := r.pidFilePath()
	_ = os.MkdirAll(filepath.Dir(path), 0755)

	data, _ := json.MarshalIndent(runnerPIDFile{
		PID:       pid,
		Mode:      mode,
		StartedAt: time.Now().Format(time.RFC3339),
	}, "", "  ")
	_ = os.WriteFile(path, data, 0644)
}

func (r *ResearchRunner) clearPIDFile() {
	if r.repoPath == "" {
		return
	}
	_ = os.Remove(r.pidFilePath())
}

func (r *ResearchRunner) readPIDFile() (runnerPIDFile, bool) {
	var pf runnerPIDFile
	if r.repoPath == "" {
		return pf, false
	}
	b, err := os.ReadFile(r.pidFilePath())
	if err != nil {
		return pf, false
	}
	if err := json.Unmarshal(b, &pf); err != nil {
		return runnerPIDFile{}, false
	}
	if pf.PID <= 0 {
		return runnerPIDFile{}, false
	}
	return pf, true
}

// Reconcile checks if a previously-started run is still running (e.g. after backend restart)
// and updates the in-memory status so the UI can stop it.
func (r *ResearchRunner) Reconcile() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.state != StateIdle {
		return
	}
	pf, ok := r.readPIDFile()
	if !ok {
		return
	}
	if isPIDRunning(pf.PID) {
		r.pid = pf.PID
		r.state = StateRunning
		r.mode = pf.Mode
		r.currentStep = fmt.Sprintf("Recovered running process (pid %d)", pf.PID)
		r.progress = 0
		now := time.Now()
		r.startTime = &now
	} else {
		r.clearPIDFile()
	}
}

func isPIDRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	if runtime.GOOS == "windows" {
		// tasklist returns 0 if it finds a matching PID
		cmd := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid))
		out, err := cmd.CombinedOutput()
		if err != nil {
			return false
		}
		return strings.Contains(string(out), strconv.Itoa(pid))
	}
	// On unix-like systems, `kill -0` checks existence.
	cmd := exec.Command("sh", "-c", fmt.Sprintf("kill -0 %d 2>/dev/null", pid))
	return cmd.Run() == nil
}

// RunnerStatus represents the current runner status
type RunnerStatus struct {
	State       RunState `json:"state"`
	Mode        RunMode  `json:"mode,omitempty"`
	CurrentStep string   `json:"currentStep,omitempty"`
	Progress    int      `json:"progress"`
	StartTime   *string  `json:"startTime,omitempty"`
	Duration    string   `json:"duration,omitempty"`
}

// RunConfig contains configuration for starting a research run
type RunConfig struct {
	Mode            RunMode `json:"mode"`
	Iterations      int     `json:"iterations"`
	MinImprovements int     `json:"minImprovements"`
	MaxAttempts     int     `json:"maxAttempts"`
	ArticleCount    int     `json:"articleCount"`
}

// NewResearchRunner creates a new research runner
func NewResearchRunner(repoPath string, broadcastFn func(StatusUpdate)) *ResearchRunner {
	return &ResearchRunner{
		state:       StateIdle,
		repoPath:    repoPath,
		broadcastFn: broadcastFn,
		pauseCh:     make(chan struct{}, 1),
		resumeCh:    make(chan struct{}, 1),
		stopCh:      make(chan struct{}, 1),
	}
}

// GetStatus returns the current runner status
func (r *ResearchRunner) GetStatus() RunnerStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()

	status := RunnerStatus{
		State:       r.state,
		Mode:        r.mode,
		CurrentStep: r.currentStep,
		Progress:    r.progress,
	}

	if r.startTime != nil {
		t := r.startTime.Format(time.RFC3339)
		status.StartTime = &t
		status.Duration = time.Since(*r.startTime).Round(time.Second).String()
	}

	return status
}

// Start begins a new research run
func (r *ResearchRunner) Start(config RunConfig) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.state != StateIdle {
		return fmt.Errorf("cannot start: runner is %s", r.state)
	}

	// Build command based on mode
	args := []string{"run", "."}
	args = append(args, "--once")
	
	if r.repoPath != "" {
		args = append(args, "--repo-path", r.repoPath)
	}
	args = append(args, "--no-commit")

	switch config.Mode {
	case ModeBackfillImages:
		args = append(args, "--backfill-images")
	case ModeGenerateImages:
		args = append(args, "--generate-images")
	}

	// Set environment variables
	env := os.Environ()
	if config.Iterations > 0 {
		env = append(env, fmt.Sprintf("TOPIC_PROCESSING_ITERATIONS=%d", config.Iterations))
	}
	if config.MinImprovements > 0 {
		env = append(env, fmt.Sprintf("IMPROVEMENTS_PER_NEW_ARTICLE=%d", config.MinImprovements))
	}
	if config.MaxAttempts > 0 {
		env = append(env, fmt.Sprintf("MAX_IMPROVEMENT_ATTEMPTS=%d", config.MaxAttempts))
	}

	// Create context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	r.cancelFunc = cancel

	// Create command
	r.cmd = exec.CommandContext(ctx, "go", args...)
	r.cmd.Dir = "." // Current directory (researcher folder)
	r.cmd.Env = env
	r.cmd.Stdout = os.Stdout
	r.cmd.Stderr = os.Stderr

	// Start immediately so we can capture PID and reliably stop later
	if err := r.cmd.Start(); err != nil {
		r.cmd = nil
		r.cancelFunc = nil
		return fmt.Errorf("failed to start research process: %w", err)
	}
	r.pid = r.cmd.Process.Pid
	r.writePIDFile(r.pid, config.Mode)

	// Update state
	r.state = StateRunning
	r.mode = config.Mode
	r.currentStep = "Initializing..."
	r.progress = 0
	now := time.Now()
	r.startTime = &now

	// Reset control channels
	r.pauseCh = make(chan struct{}, 1)
	r.resumeCh = make(chan struct{}, 1)
	r.stopCh = make(chan struct{}, 1)

	// Start process in background
	go r.runProcess()

	r.broadcast("researcher", r.getStatusUnlocked())

	log.Printf("[Runner] Started research run in mode: %s", config.Mode)
	return nil
}

func (r *ResearchRunner) runProcess() {
	err := r.cmd.Wait()

	r.mu.Lock()
	defer r.mu.Unlock()

	if err != nil {
		log.Printf("[Runner] Process exited with error: %v", err)
		r.currentStep = fmt.Sprintf("Error: %v", err)
	} else {
		r.currentStep = "Completed"
		r.progress = 100
	}

	r.state = StateIdle
	r.mode = ""
	r.cmd = nil
	r.cancelFunc = nil
	r.pid = 0
	r.clearPIDFile()

	r.broadcast("researcher", r.getStatusUnlocked())
}

// Pause pauses the current run (sends SIGSTOP on Unix, not fully supported on Windows)
func (r *ResearchRunner) Pause() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.state != StateRunning {
		return fmt.Errorf("cannot pause: runner is %s", r.state)
	}

	if r.cmd != nil && r.cmd.Process != nil {
		// On Unix, we could send SIGSTOP, but for cross-platform compatibility,
		// we'll just mark as paused and let the process continue
		// A future enhancement could implement proper pause/resume
		r.state = StatePaused
		r.broadcast("researcher", r.getStatusUnlocked())
		log.Println("[Runner] Paused (note: process continues in background)")
	}

	return nil
}

// Resume resumes a paused run
func (r *ResearchRunner) Resume() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.state != StatePaused {
		return fmt.Errorf("cannot resume: runner is %s", r.state)
	}

	r.state = StateRunning
	r.broadcast("researcher", r.getStatusUnlocked())
	log.Println("[Runner] Resumed")

	return nil
}

// Stop stops the current run
func (r *ResearchRunner) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.state == StateIdle {
		// If backend restarted, we may still have an orphaned process recorded on disk.
		if pf, ok := r.readPIDFile(); ok {
			_ = killPIDTree(pf.PID)
			r.clearPIDFile()
			r.currentStep = "Stopped orphaned run"
			r.pid = 0
			return nil
		}

		// Extra safety: if PID file is missing/outdated, try to find and kill stray researcher runs.
		if killed := r.killOrphanedResearchRunsUnlocked(); killed > 0 {
			r.currentStep = fmt.Sprintf("Stopped %d orphaned run(s)", killed)
		}
		return nil
	}

	r.state = StateStopping
	r.broadcast("researcher", r.getStatusUnlocked())

	if r.cancelFunc != nil {
		r.cancelFunc()
	}

	// Give process time to clean up gracefully
	go func() {
		time.Sleep(5 * time.Second)
		r.mu.Lock()
		defer r.mu.Unlock()
		
		if r.cmd != nil && r.cmd.Process != nil {
			log.Printf("[Runner] Forcefully terminating process PID %d", r.cmd.Process.Pid)
			_ = killPIDTree(r.cmd.Process.Pid)
			
			// Update state after killing
			r.state = StateIdle
			r.currentStep = "Stopped by user"
			r.pid = 0
			r.clearPIDFile()
			r.broadcast("researcher", r.getStatusUnlocked())
		}
	}()

	log.Println("[Runner] Stopping...")
	return nil
}

func killPIDTree(pid int) error {
	if pid <= 0 {
		return nil
	}
	if runtime.GOOS == "windows" {
		// Kill process tree
		killCmd := exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprintf("%d", pid))
		return killCmd.Run()
	}
	// Best-effort kill
	cmd := exec.Command("sh", "-c", fmt.Sprintf("kill -TERM %d 2>/dev/null || true", pid))
	_ = cmd.Run()
	cmd = exec.Command("sh", "-c", fmt.Sprintf("kill -KILL %d 2>/dev/null || true", pid))
	return cmd.Run()
}

// killOrphanedResearchRunsUnlocked tries to find and kill researcher subprocesses started via the dashboard
// even if we lost the PID (e.g., PID file removed or backend restarted mid-write).
// Must be called with r.mu held.
func (r *ResearchRunner) killOrphanedResearchRunsUnlocked() int {
	if runtime.GOOS != "windows" {
		return 0
	}
	if r.repoPath == "" {
		return 0
	}

	// Use PowerShell to find go.exe processes whose command line matches our repo path and dashboard run pattern.
	// Then taskkill each PID with /T to kill process tree.
	ps := fmt.Sprintf(`$repo=%q; `+
		`$pids=Get-CimInstance Win32_Process | Where-Object { `+
		`$_.Name -eq 'go.exe' -and $_.CommandLine -match 'go run \. --once' -and $_.CommandLine -like ('*' + $repo + '*') } `+
		`| Select-Object -ExpandProperty ProcessId; `+
		`$k=0; foreach($p in $pids){ try{ taskkill /F /T /PID $p | Out-Null; $k++ }catch{} }; `+
		`Write-Output $k`, r.repoPath)

	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", ps).CombinedOutput()
	if err != nil {
		log.Printf("[Runner] Failed to kill orphaned runs via PowerShell: %v (%s)", err, string(out))
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	return n
}

// UpdateStep updates the current step (called by agent during execution)
func (r *ResearchRunner) UpdateStep(step string, progress int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.currentStep = step
	r.progress = progress
	r.broadcast("researcher", r.getStatusUnlocked())
}

func (r *ResearchRunner) getStatusUnlocked() RunnerStatus {
	status := RunnerStatus{
		State:       r.state,
		Mode:        r.mode,
		CurrentStep: r.currentStep,
		Progress:    r.progress,
	}

	if r.startTime != nil {
		t := r.startTime.Format(time.RFC3339)
		status.StartTime = &t
		status.Duration = time.Since(*r.startTime).Round(time.Second).String()
	}

	return status
}

func (r *ResearchRunner) broadcast(updateType string, payload interface{}) {
	if r.broadcastFn != nil {
		r.broadcastFn(StatusUpdate{
			Type:    updateType,
			Payload: payload,
		})
	}
}

// Service control helpers

// StartDocker attempts to start Docker Desktop
func StartDocker() error {
	if runtime.GOOS == "windows" {
		dockerPath := os.Getenv("DOCKER_DESKTOP_PATH")
		if dockerPath == "" {
			dockerPath = `C:\Program Files\Docker\Docker\Docker Desktop.exe`
		}
		
		cmd := exec.Command(dockerPath)
		return cmd.Start()
	}
	return fmt.Errorf("Docker auto-start not supported on %s", runtime.GOOS)
}

// StopDocker stops Docker Desktop (not commonly needed)
func StopDocker() error {
	if runtime.GOOS == "windows" {
		cmd := exec.Command("taskkill", "/IM", "Docker Desktop.exe", "/F")
		return cmd.Run()
	}
	return fmt.Errorf("Docker stop not supported on %s", runtime.GOOS)
}

// StartOllama starts the Ollama service
func StartOllama() error {
	startCmd := os.Getenv("OLLAMA_START_CMD")
	if startCmd == "" {
		startCmd = "docker compose start ollama"
	}

	// Check if this is a native command (not docker compose)
	if !strings.Contains(startCmd, "docker") && !strings.Contains(startCmd, "compose") {
		return runNativeCmd(startCmd)
	}

	return runDockerComposeCmd(startCmd)
}

// StopOllama stops the Ollama service
func StopOllama() error {
	stopCmd := os.Getenv("OLLAMA_STOP_CMD")
	if stopCmd == "" {
		stopCmd = "docker compose stop ollama"
	}

	// Check if this is a native command (not docker compose)
	if !strings.Contains(stopCmd, "docker") && !strings.Contains(stopCmd, "compose") {
		// Native stop command
		_ = runNativeCmd(stopCmd)
		// If something is still listening, also stop native Ollama on Windows.
		if runtime.GOOS == "windows" && isListening("127.0.0.1:11434") {
			_ = stopNativeOllamaWindows()
			time.Sleep(500 * time.Millisecond)
			if isListening("127.0.0.1:11434") {
				return fmt.Errorf("ollama still listening on 11434 after stop")
			}
		}
		return nil
	}

	// Try docker compose stop (best effort). If docker isn't running or no project is up,
	// this may fail even though a native Ollama is running. We still want the Stop button
	// to stop whichever Ollama is actually serving on localhost:11434.
	dockerErr := runDockerComposeCmd(stopCmd)

	// If something is still listening, stop native Ollama on Windows.
	if runtime.GOOS == "windows" && isListening("127.0.0.1:11434") {
		_ = stopNativeOllamaWindows()
		time.Sleep(500 * time.Millisecond)
	}

	// Decide success based on whether port is still listening.
	if runtime.GOOS == "windows" && isListening("127.0.0.1:11434") {
		if dockerErr != nil {
			return fmt.Errorf("failed to stop ollama (docker compose error: %v) and native ollama is still listening on 11434", dockerErr)
		}
		return fmt.Errorf("failed to stop ollama: still listening on 11434")
	}

	// If docker compose errored but nothing is listening, treat stop as successful.
	return nil
}

// StartComfyUI starts the ComfyUI service
func StartComfyUI() error {
	startCmd := os.Getenv("COMFYUI_START_CMD")
	if startCmd == "" {
		startCmd = "docker compose up -d comfyui"
	}

	return runDockerComposeCmd(startCmd)
}

// StopComfyUI stops the ComfyUI service
func StopComfyUI() error {
	stopCmd := os.Getenv("COMFYUI_STOP_CMD")
	if stopCmd == "" {
		stopCmd = "docker compose stop comfyui"
	}

	return runDockerComposeCmd(stopCmd)
}

func runDockerComposeCmd(cmdStr string) error {
	workDir := os.Getenv("DOCKER_COMPOSE_DIR")
	if workDir == "" {
		workDir = "infra"
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/C", cmdStr)
	} else {
		cmd = exec.Command("sh", "-c", cmdStr)
	}
	cmd.Dir = workDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// runNativeCmd runs a native command (not docker compose)
func runNativeCmd(cmdStr string) error {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/C", cmdStr)
	} else {
		cmd = exec.Command("sh", "-c", cmdStr)
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

func isListening(addr string) bool {
	c, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// stopNativeOllamaWindows stops a native Windows Ollama process (not docker).
// It targets the PID that is actually listening on 11434.
func stopNativeOllamaWindows() error {
	// Get owning PID(s) for port 11434 and kill the process tree.
	ps := `Get-NetTCPConnection -LocalPort 11434 -State Listen -ErrorAction SilentlyContinue | ` +
		`Select-Object -ExpandProperty OwningProcess -Unique`
	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", ps).CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to query Ollama listener PID: %w (%s)", err, string(out))
	}
	pids := strings.Fields(string(out))
	if len(pids) == 0 {
		// Fall back to killing by image name
		_ = exec.Command("taskkill", "/F", "/IM", "ollama.exe").Run()
		return nil
	}
	for _, pid := range pids {
		_ = exec.Command("taskkill", "/F", "/T", "/PID", pid).Run()
	}
	return nil
}
