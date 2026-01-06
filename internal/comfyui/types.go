// Package comfyui provides a client for interacting with the ComfyUI API
package comfyui

// GenerateOptions configures image generation parameters
type GenerateOptions struct {
	Model       string  // Diffusion model filename
	TextEncoder string  // Text encoder filename
	VAE         string  // VAE filename
	Width       int     // Image width
	Height      int     // Image height
	Steps       int     // Number of sampling steps
	CFG         float64 // Classifier-free guidance scale
	Seed        int64   // Random seed (-1 for random)
	Sampler     string  // Sampler name (euler, dpm++, etc.)
	Scheduler   string  // Scheduler name (normal, karras, etc.)
}

// DefaultOptions returns sensible default options for Qwen-Image FP8
func DefaultOptions() GenerateOptions {
	return GenerateOptions{
		Model:       "qwen_image_2512_fp8_e4m3fn.safetensors",
		TextEncoder: "qwen_2.5_vl_7b_fp8_scaled.safetensors",
		VAE:         "qwen_image_vae.safetensors",
		Width:       1024,
		Height:      1024,
		Steps:       20,
		CFG:         4.5,
		Seed:        -1, // Random
		Sampler:     "euler",
		Scheduler:   "normal",
	}
}

// PromptRequest is the request body for queueing a prompt
type PromptRequest struct {
	Prompt   map[string]interface{} `json:"prompt"`
	ClientID string                 `json:"client_id,omitempty"`
}

// PromptResponse is the response from queueing a prompt
type PromptResponse struct {
	PromptID string `json:"prompt_id"`
}

// HistoryResponse contains the execution history for a prompt
type HistoryResponse map[string]HistoryEntry

// HistoryEntry represents a single execution in history
type HistoryEntry struct {
	Prompt  []interface{}          `json:"prompt"`
	Outputs map[string]OutputNode  `json:"outputs"`
	Status  map[string]interface{} `json:"status"`
}

// OutputNode represents output from a node
type OutputNode struct {
	Images []ImageOutput `json:"images,omitempty"`
}

// ImageOutput represents a generated image
type ImageOutput struct {
	Filename  string `json:"filename"`
	Subfolder string `json:"subfolder"`
	Type      string `json:"type"`
}

// SystemStats contains system information from ComfyUI
type SystemStats struct {
	System struct {
		OS             string `json:"os"`
		PythonVersion  string `json:"python_version"`
		ComfyUIVersion string `json:"comfyui_version"`
	} `json:"system"`
	Devices []struct {
		Name      string `json:"name"`
		Type      string `json:"type"`
		VRAMTotal int64  `json:"vram_total"`
		VRAMFree  int64  `json:"vram_free"`
	} `json:"devices"`
}



