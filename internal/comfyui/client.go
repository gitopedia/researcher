package comfyui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"
)

// Client provides methods to interact with ComfyUI's API
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a new ComfyUI client
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Minute, // Long timeout for image generation
		},
	}
}

// getEnvOrDefault returns environment variable value or default
func getEnvOrDefault(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

// IsHealthy checks if ComfyUI is running and responsive
func (c *Client) IsHealthy(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/system_stats", nil)
	if err != nil {
		return false
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}

// GetSystemStats returns system information from ComfyUI
func (c *Client) GetSystemStats(ctx context.Context) (*SystemStats, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/system_stats", nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	var stats SystemStats
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		return nil, err
	}

	return &stats, nil
}

// QueuePrompt sends a workflow to ComfyUI for execution
func (c *Client) QueuePrompt(ctx context.Context, workflow map[string]interface{}) (string, error) {
	reqBody := PromptRequest{
		Prompt: workflow,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/prompt", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("queue prompt failed with status %d: %s", resp.StatusCode, string(body))
	}

	var promptResp PromptResponse
	if err := json.NewDecoder(resp.Body).Decode(&promptResp); err != nil {
		return "", err
	}

	return promptResp.PromptID, nil
}

// GetHistory retrieves execution history for a prompt
func (c *Client) GetHistory(ctx context.Context, promptID string) (*HistoryEntry, error) {
	url := fmt.Sprintf("%s/history/%s", c.baseURL, promptID)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get history failed with status %d", resp.StatusCode)
	}

	var history HistoryResponse
	if err := json.NewDecoder(resp.Body).Decode(&history); err != nil {
		return nil, err
	}

	entry, ok := history[promptID]
	if !ok {
		return nil, nil // Not complete yet
	}

	return &entry, nil
}

// GetImage downloads a generated image
func (c *Client) GetImage(ctx context.Context, filename, subfolder, imgType string) ([]byte, error) {
	url := fmt.Sprintf("%s/view?filename=%s&subfolder=%s&type=%s", c.baseURL, filename, subfolder, imgType)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get image failed with status %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

// WaitForCompletion polls until the prompt is complete
func (c *Client) WaitForCompletion(ctx context.Context, promptID string, pollInterval time.Duration) (*HistoryEntry, error) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			entry, err := c.GetHistory(ctx, promptID)
			if err != nil {
				return nil, err
			}
			if entry != nil {
				return entry, nil
			}
		}
	}
}

// GenerateImage is a high-level method that generates an image from a prompt
func (c *Client) GenerateImage(ctx context.Context, prompt string, opts *GenerateOptions) ([]byte, error) {
	if opts == nil {
		defaults := DefaultOptions()
		opts = &defaults
	}

	// Override with environment variables if set
	opts.Model = getEnvOrDefault("COMFYUI_MODEL", opts.Model)
	opts.TextEncoder = getEnvOrDefault("COMFYUI_TEXT_ENCODER", opts.TextEncoder)
	opts.VAE = getEnvOrDefault("COMFYUI_VAE", opts.VAE)

	log.Printf("ComfyUI: Generating image with prompt: %.100s...", prompt)
	log.Printf("ComfyUI: Using model: %s", opts.Model)

	workflow := BuildTextToImageWorkflow(prompt, *opts)

	promptID, err := c.QueuePrompt(ctx, workflow)
	if err != nil {
		return nil, fmt.Errorf("failed to queue prompt: %w", err)
	}
	log.Printf("ComfyUI: Queued prompt %s", promptID)

	entry, err := c.WaitForCompletion(ctx, promptID, 2*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed waiting for completion: %w", err)
	}

	// Find the SaveImage output (node 9)
	output, ok := entry.Outputs["9"]
	if !ok || len(output.Images) == 0 {
		return nil, fmt.Errorf("no images in output")
	}

	img := output.Images[0]
	log.Printf("ComfyUI: Generated image: %s", img.Filename)

	imageData, err := c.GetImage(ctx, img.Filename, img.Subfolder, img.Type)
	if err != nil {
		return nil, fmt.Errorf("failed to download image: %w", err)
	}

	return imageData, nil
}




