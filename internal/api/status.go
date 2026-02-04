package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// StatusManager handles checking status of various services
type StatusManager struct{}

// NewStatusManager creates a new status manager
func NewStatusManager() *StatusManager {
	return &StatusManager{}
}

// FullStatus represents the complete system status
type FullStatus struct {
	Docker   DockerStatus   `json:"docker"`
	Ollama   OllamaStatus   `json:"ollama"`
	ComfyUI  ComfyUIStatus  `json:"comfyui"`
	Hardware HardwareStatus `json:"hardware"`
}

// DockerStatus represents Docker Desktop status
type DockerStatus struct {
	Running   bool   `json:"running"`
	Version   string `json:"version,omitempty"`
	Error     string `json:"error,omitempty"`
}

// OllamaStatus represents Ollama service status
type OllamaStatus struct {
	Running     bool     `json:"running"`
	Models      []string `json:"models,omitempty"`
	LoadedModel string   `json:"loadedModel,omitempty"`
	Error       string   `json:"error,omitempty"`
}

// ComfyUIStatus represents ComfyUI service status
type ComfyUIStatus struct {
	Running bool   `json:"running"`
	Version string `json:"version,omitempty"`
	Error   string `json:"error,omitempty"`
}

// HardwareStatus represents hardware resource status
type HardwareStatus struct {
	GPU       *GPUStatus `json:"gpu,omitempty"`
	RAM       RAMStatus  `json:"ram"`
	CPUUsage  float64    `json:"cpuUsage"`
}

// GPUStatus represents GPU/VRAM status
type GPUStatus struct {
	Name        string  `json:"name"`
	VRAMTotal   int64   `json:"vramTotal"`   // bytes
	VRAMUsed    int64   `json:"vramUsed"`    // bytes
	VRAMFree    int64   `json:"vramFree"`    // bytes
	Utilization float64 `json:"utilization"` // percentage
	Temperature int     `json:"temperature"` // celsius
}

// RAMStatus represents system RAM status
type RAMStatus struct {
	Total int64 `json:"total"` // bytes
	Used  int64 `json:"used"`  // bytes
	Free  int64 `json:"free"`  // bytes
}

// GetFullStatus returns the complete system status
func (m *StatusManager) GetFullStatus() FullStatus {
	return FullStatus{
		Docker:   m.GetDockerStatus(),
		Ollama:   m.GetOllamaStatus(),
		ComfyUI:  m.GetComfyUIStatus(),
		Hardware: m.GetHardwareStatus(),
	}
}

// GetDockerStatus checks if Docker is running
func (m *StatusManager) GetDockerStatus() DockerStatus {
	cmd := exec.Command("docker", "info", "--format", "{{.ServerVersion}}")
	output, err := cmd.Output()
	if err != nil {
		return DockerStatus{
			Running: false,
			Error:   "Docker not running or not installed",
		}
	}
	return DockerStatus{
		Running: true,
		Version: strings.TrimSpace(string(output)),
	}
}

// GetOllamaStatus checks Ollama service status
func (m *StatusManager) GetOllamaStatus() OllamaStatus {
	baseURL := os.Getenv("LLM_BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:11434/v1"
	}
	// Remove /v1 suffix for native API
	baseURL = strings.TrimSuffix(baseURL, "/v1")

	client := &http.Client{Timeout: 3 * time.Second}

	// Check if Ollama is running
	resp, err := client.Get(baseURL + "/api/tags")
	if err != nil {
		return OllamaStatus{
			Running: false,
			Error:   "Ollama not responding",
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return OllamaStatus{
			Running: false,
			Error:   fmt.Sprintf("Ollama returned status %d", resp.StatusCode),
		}
	}

	// Parse models list
	var tagsResp struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tagsResp); err == nil {
		var models []string
		for _, m := range tagsResp.Models {
			models = append(models, m.Name)
		}

		// Check for loaded model via /api/ps
		loadedModel := ""
		psResp, err := client.Get(baseURL + "/api/ps")
		if err == nil {
			defer psResp.Body.Close()
			var psData struct {
				Models []struct {
					Name string `json:"name"`
				} `json:"models"`
			}
			if json.NewDecoder(psResp.Body).Decode(&psData) == nil && len(psData.Models) > 0 {
				loadedModel = psData.Models[0].Name
			}
		}

		return OllamaStatus{
			Running:     true,
			Models:      models,
			LoadedModel: loadedModel,
		}
	}

	return OllamaStatus{Running: true}
}

// GetComfyUIStatus checks ComfyUI service status
func (m *StatusManager) GetComfyUIStatus() ComfyUIStatus {
	comfyURL := os.Getenv("COMFYUI_URL")
	if comfyURL == "" {
		comfyURL = "http://localhost:8188"
	}

	client := &http.Client{Timeout: 3 * time.Second}

	resp, err := client.Get(comfyURL + "/system_stats")
	if err != nil {
		return ComfyUIStatus{
			Running: false,
			Error:   "ComfyUI not responding",
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ComfyUIStatus{
			Running: false,
			Error:   fmt.Sprintf("ComfyUI returned status %d", resp.StatusCode),
		}
	}

	var stats struct {
		System struct {
			ComfyUIVersion string `json:"comfyui_version"`
		} `json:"system"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&stats); err == nil {
		return ComfyUIStatus{
			Running: true,
			Version: stats.System.ComfyUIVersion,
		}
	}

	return ComfyUIStatus{Running: true}
}

// GetHardwareStatus gets hardware resource status
func (m *StatusManager) GetHardwareStatus() HardwareStatus {
	status := HardwareStatus{
		RAM: m.getRAMStatus(),
	}

	// Try to get GPU status via nvidia-smi
	gpuStatus := m.getNvidiaGPUStatus()
	if gpuStatus != nil {
		status.GPU = gpuStatus
	}

	return status
}

// getNvidiaGPUStatus gets NVIDIA GPU status via nvidia-smi
func (m *StatusManager) getNvidiaGPUStatus() *GPUStatus {
	// Query nvidia-smi for GPU info
	cmd := exec.Command("nvidia-smi",
		"--query-gpu=name,memory.total,memory.used,memory.free,utilization.gpu,temperature.gpu",
		"--format=csv,noheader,nounits")

	output, err := cmd.Output()
	if err != nil {
		return nil
	}

	// Parse CSV output: name, total, used, free, util, temp
	parts := strings.Split(strings.TrimSpace(string(output)), ", ")
	if len(parts) < 6 {
		return nil
	}

	parseMemMB := func(s string) int64 {
		v, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
		return v * 1024 * 1024 // Convert MB to bytes
	}

	parseFloat := func(s string) float64 {
		v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
		return v
	}

	parseInt := func(s string) int {
		v, _ := strconv.Atoi(strings.TrimSpace(s))
		return v
	}

	return &GPUStatus{
		Name:        strings.TrimSpace(parts[0]),
		VRAMTotal:   parseMemMB(parts[1]),
		VRAMUsed:    parseMemMB(parts[2]),
		VRAMFree:    parseMemMB(parts[3]),
		Utilization: parseFloat(parts[4]),
		Temperature: parseInt(parts[5]),
	}
}

// getRAMStatus gets system RAM status
func (m *StatusManager) getRAMStatus() RAMStatus {
	var status RAMStatus

	if runtime.GOOS == "windows" {
		// Use wmic on Windows
		cmd := exec.Command("wmic", "OS", "get", "FreePhysicalMemory,TotalVisibleMemorySize", "/Value")
		output, err := cmd.Output()
		if err == nil {
			lines := strings.Split(string(output), "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "FreePhysicalMemory=") {
					val := strings.TrimPrefix(line, "FreePhysicalMemory=")
					v, _ := strconv.ParseInt(strings.TrimSpace(val), 10, 64)
					status.Free = v * 1024 // KB to bytes
				} else if strings.HasPrefix(line, "TotalVisibleMemorySize=") {
					val := strings.TrimPrefix(line, "TotalVisibleMemorySize=")
					v, _ := strconv.ParseInt(strings.TrimSpace(val), 10, 64)
					status.Total = v * 1024 // KB to bytes
				}
			}
			status.Used = status.Total - status.Free
		}
	} else {
		// Use /proc/meminfo on Linux
		data, err := os.ReadFile("/proc/meminfo")
		if err == nil {
			lines := strings.Split(string(data), "\n")
			for _, line := range lines {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					val, _ := strconv.ParseInt(fields[1], 10, 64)
					val *= 1024 // KB to bytes
					switch fields[0] {
					case "MemTotal:":
						status.Total = val
					case "MemFree:":
						status.Free = val
					}
				}
			}
			status.Used = status.Total - status.Free
		}
	}

	return status
}
