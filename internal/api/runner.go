package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
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
	StateIdle     RunState = "idle"
	StateRunning  RunState = "running"
	StatePaused   RunState = "paused"
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
	mu          sync.RWMutex
	state       RunState
	mode        RunMode
	currentStep string
	progress    int // 0-100
	startTime   *time.Time
	pid         int

	// Control channels
	pauseCh  chan struct{}
	resumeCh chan struct{}
	stopCh   chan struct{}

	// Process management
	cmd        *exec.Cmd
	cancelFunc context.CancelFunc

	repoPath    string
	broadcastFn func(StatusUpdate)
}

type StopResult struct {
	Status        string `json:"status"`
	KilledPIDs    []int  `json:"killedPids,omitempty"`
	RemainingPIDs []int  `json:"remainingPids,omitempty"`
	Message       string `json:"message,omitempty"`
}

type runnerPIDFile struct {
	PID       int     `json:"pid"`
	Mode      RunMode `json:"mode"`
	StartedAt string  `json:"startedAt"`
}

func (r *ResearchRunner) pidFilePath() string {
	return filepath.Join(r.repoPath, "Compendium", "_debug", "dashboard_runner_pid.json")
}

func (r *ResearchRunner) pauseFlagPath() string {
	if strings.TrimSpace(r.repoPath) == "" {
		return ""
	}
	return filepath.Join(r.repoPath, "Compendium", "_debug", "dashboard_pause.flag")
}

func (r *ResearchRunner) setPauseFlagUnlocked() error {
	p := r.pauseFlagPath()
	if p == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return err
	}
	// Contents are informational only; existence is what matters.
	return os.WriteFile(p, []byte("paused\n"), 0644)
}

func (r *ResearchRunner) clearPauseFlagUnlocked() {
	p := r.pauseFlagPath()
	if p == "" {
		return
	}
	_ = os.Remove(p)
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

	// Ensure we don't start in a paused state from a stale flag.
	r.clearPauseFlagUnlocked()

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

	// Always clear pause flag when the process exits so the next run isn't blocked.
	r.clearPauseFlagUnlocked()

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
		// For cross-platform compatibility, we pause cooperatively via a shared flag file.
		// The running agent checks this flag at safe boundaries and blocks until it's cleared.
		if err := r.setPauseFlagUnlocked(); err != nil {
			return fmt.Errorf("failed to set pause flag: %w", err)
		}
		r.state = StatePaused
		r.broadcast("researcher", r.getStatusUnlocked())
		log.Println("[Runner] Paused (cooperative pause flag set)")
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

	r.clearPauseFlagUnlocked()
	r.state = StateRunning
	r.broadcast("researcher", r.getStatusUnlocked())
	log.Println("[Runner] Resumed")

	return nil
}

// Stop stops the current run
func (r *ResearchRunner) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Best-effort: never leave a stale pause flag behind.
	r.clearPauseFlagUnlocked()

	if r.state == StateIdle {
		var killed int
		var errs []string

		// If backend restarted, we may still have an orphaned process recorded on disk.
		if pf, ok := r.readPIDFile(); ok {
			if err := killPIDTree(pf.PID); err != nil {
				errs = append(errs, fmt.Sprintf("failed to kill pid from pidfile (%d): %v", pf.PID, err))
			} else {
				killed++
			}
			r.clearPIDFile()
			r.pid = 0
		}

		// Extra safety: if PID file is missing/outdated, try to find and kill stray researcher runs.
		killed += r.killOrphanedResearchRunsUnlocked()

		// Verify no orphaned runs remain. If they do, surface an error so UI can display it.
		remaining := r.findOrphanedResearchRunsUnlocked()
		if len(remaining) > 0 {
			errs = append(errs, fmt.Sprintf("still running: %v", remaining))
		}

		if killed > 0 {
			r.currentStep = fmt.Sprintf("Stopped %d run(s)", killed)
		} else if len(errs) == 0 {
			r.currentStep = "No run to stop"
		}

		if len(errs) > 0 {
			return fmt.Errorf("%s", strings.Join(errs, "; "))
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
			r.clearPauseFlagUnlocked()
			r.broadcast("researcher", r.getStatusUnlocked())
		}
	}()

	log.Println("[Runner] Stopping...")
	return nil
}

// ForceStop immediately kills the active run process (if any) and any orphaned dashboard runs.
// This is used when the run is stuck (e.g. waiting on network/ollama) or the backend lost state.
func (r *ResearchRunner) ForceStop() StopResult {
	r.mu.Lock()
	defer r.mu.Unlock()

	var killed []int
	r.clearPauseFlagUnlocked()

	// Kill PID recorded on disk (if present)
	if pf, ok := r.readPIDFile(); ok {
		_ = killPIDTree(pf.PID)
		killed = append(killed, pf.PID)
		r.clearPIDFile()
		r.pid = 0
	}

	// Kill active command PID (if any)
	if r.cmd != nil && r.cmd.Process != nil {
		_ = killPIDTree(r.cmd.Process.Pid)
		killed = append(killed, r.cmd.Process.Pid)
	}

	// Cancel context as well (best-effort)
	if r.cancelFunc != nil {
		r.cancelFunc()
	}

	// Kill any other orphaned dashboard runs
	_ = r.killOrphanedResearchRunsUnlocked()

	remaining := r.findOrphanedResearchRunsUnlocked()

	// Update visible state
	if len(remaining) == 0 {
		r.state = StateIdle
		r.mode = ""
		r.currentStep = "Force-stopped"
		r.progress = 0
		r.startTime = nil
	} else {
		r.state = StateStopping
		r.currentStep = fmt.Sprintf("Force-stop attempted; %d process(es) still running", len(remaining))
	}

	r.broadcast("researcher", r.getStatusUnlocked())

	return StopResult{
		Status:        "force-stopped",
		KilledPIDs:    uniqueInts(killed),
		RemainingPIDs: remaining,
		Message:       r.currentStep,
	}
}

func uniqueInts(in []int) []int {
	seen := make(map[int]bool)
	var out []int
	for _, v := range in {
		if v <= 0 {
			continue
		}
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
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

	// First, find candidate PIDs via PowerShell. Then kill each PID via taskkill from Go.
	// (Doing the kill in PowerShell can falsely "succeed" because taskkill failures don't always throw.)
	pids := r.findOrphanedResearchRunsUnlocked()
	killed := 0
	for _, pid := range pids {
		if err := killPIDTree(pid); err != nil {
			log.Printf("[Runner] Failed to kill orphaned run pid=%d: %v", pid, err)
			continue
		}
		killed++
	}
	return killed
}

// findOrphanedResearchRunsUnlocked returns PIDs of likely orphaned dashboard runs.
// Must be called with r.mu held.
func (r *ResearchRunner) findOrphanedResearchRunsUnlocked() []int {
	if runtime.GOOS != "windows" || r.repoPath == "" {
		return nil
	}
	repo2 := strings.ReplaceAll(r.repoPath, `\`, `/`)
	// Match both go.exe and temp researcher.exe; require --once and --repo-path for this repo.
	ps := fmt.Sprintf(`$repo=%q; $repo2=%q; `+
		`Get-CimInstance Win32_Process | Where-Object { `+
		`( $_.Name -eq 'go.exe' -or $_.Name -eq 'researcher.exe' ) -and `+
		// NOTE: --once starts with '-' (a non-word character), so regex word-boundaries (\b) do NOT match it.
		// Use explicit whitespace / start/end checks instead.
		`$_.CommandLine -match '(?i)(^|\s)--once(\s|$)' -and `+
		`( $_.CommandLine -like ('*--repo-path ' + $repo + '*') -or $_.CommandLine -like ('*--repo-path ' + $repo2 + '*') ) } `+
		`| Select-Object -ExpandProperty ProcessId`, r.repoPath, repo2)
	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", ps).CombinedOutput()
	if err != nil {
		return nil
	}
	var pids []int
	for _, f := range strings.Fields(string(out)) {
		if n, err := strconv.Atoi(strings.TrimSpace(f)); err == nil && n > 0 {
			pids = append(pids, n)
		}
	}
	return pids
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

