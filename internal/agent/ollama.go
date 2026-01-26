package agent

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// getOllamaBaseURL returns the Ollama API base URL from config
func getOllamaBaseURL() string {
	baseURL := os.Getenv("LLM_BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:11434/v1"
	}
	// Remove /v1 suffix for native API calls
	return strings.TrimSuffix(baseURL, "/v1")
}

// isOllamaRunning checks if Ollama is responding to API requests
func isOllamaRunning() bool {
	baseURL := getOllamaBaseURL()
	client := &http.Client{Timeout: 3 * time.Second}

	resp, err := client.Get(baseURL + "/api/tags")
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}

// startOllamaProcess attempts to start Ollama as a background process
func startOllamaProcess() error {
	log.Println("[Ollama] Attempting to start Ollama...")

	// Check for custom start command first
	startCmd := os.Getenv("OLLAMA_START_CMD")
	if startCmd != "" {
		log.Printf("[Ollama] Using custom start command: %s", startCmd)
		var cmd *exec.Cmd
		if runtime.GOOS == "windows" {
			cmd = exec.Command("powershell", "-Command", startCmd)
		} else {
			cmd = exec.Command("sh", "-c", startCmd)
		}
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	// Try to start ollama serve directly
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		// On Windows, start ollama serve in background
		cmd = exec.Command("cmd", "/C", "start", "/B", "ollama", "serve")
	} else {
		// On Unix, start ollama serve in background
		cmd = exec.Command("sh", "-c", "ollama serve &")
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start ollama: %w", err)
	}

	log.Println("[Ollama] Started ollama serve process")
	return nil
}

// waitForOllama waits for Ollama to become available
func waitForOllama(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		if isOllamaRunning() {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}

	return fmt.Errorf("ollama did not become available within %v", timeout)
}

// EnsureOllamaRunning checks if Ollama is running and starts it if not.
// Returns an error if Ollama cannot be started or doesn't become available.
func EnsureOllamaRunning() error {
	if isOllamaRunning() {
		log.Println("[Ollama] Ollama is already running")
		return nil
	}

	log.Println("[Ollama] Ollama is not running, attempting to start...")

	if err := startOllamaProcess(); err != nil {
		return fmt.Errorf("failed to start Ollama: %w", err)
	}

	// Wait for Ollama to become available (up to 30 seconds)
	log.Println("[Ollama] Waiting for Ollama to become available...")
	if err := waitForOllama(30 * time.Second); err != nil {
		return fmt.Errorf("Ollama started but failed to become available: %w", err)
	}

	log.Println("[Ollama] Ollama is now running")
	return nil
}
