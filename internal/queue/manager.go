package queue

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

// Manager holds the LLM and ComfyUI queues and provides a unified interface.
type Manager struct {
	LLM     *Queue
	ComfyUI *Queue
}

// ManagerStatus is the combined status of both queues.
type ManagerStatus struct {
	LLM     Stats `json:"llm"`
	ComfyUI Stats `json:"comfyui"`
}

// NewManager creates queues for LLM and ComfyUI with default health checks.
func NewManager(logger *slog.Logger) *Manager {
	llmLogger := logger.With("queue", "llm")
	comfyLogger := logger.With("queue", "comfyui")

	return &Manager{
		LLM: New("llm", llmHealthCheck,
			WithLogger(llmLogger),
			WithRetryDelay(5*time.Second),
			WithMaxRetryDelay(2*time.Minute),
		),
		ComfyUI: New("comfyui", comfyUIHealthCheck,
			WithLogger(comfyLogger),
			WithRetryDelay(5*time.Second),
			WithMaxRetryDelay(2*time.Minute),
		),
	}
}

// Start begins processing on both queues.
func (m *Manager) Start(ctx context.Context) {
	m.LLM.Start(ctx)
	m.ComfyUI.Start(ctx)
}

// Stop signals both queues to drain and exit.
func (m *Manager) Stop() {
	m.LLM.Stop()
	m.ComfyUI.Stop()
}

// GetStatus returns the combined status of both queues.
func (m *Manager) GetStatus() ManagerStatus {
	return ManagerStatus{
		LLM:     m.LLM.GetStats(),
		ComfyUI: m.ComfyUI.GetStats(),
	}
}

// llmHealthCheck pings the LLM endpoint to determine availability.
func llmHealthCheck(ctx context.Context) bool {
	baseURL := os.Getenv("LLM_BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:11434/v1"
	}
	// Strip /v1 for the native health endpoint
	baseURL = strings.TrimSuffix(baseURL, "/v1")

	client := &http.Client{Timeout: 3 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/api/tags", nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// comfyUIHealthCheck pings ComfyUI to determine availability.
func comfyUIHealthCheck(ctx context.Context) bool {
	comfyURL := os.Getenv("COMFYUI_URL")
	if comfyURL == "" {
		comfyURL = "http://localhost:8188"
	}

	client := &http.Client{Timeout: 3 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", comfyURL+"/system_stats", nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
