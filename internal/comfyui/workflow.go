package comfyui

import (
	"math/rand"
	"time"
)

// BuildTextToImageWorkflow creates a ComfyUI workflow JSON for text-to-image generation
// using the Qwen-Image FP8 model. This workflow uses standard loaders for safetensors format.
func BuildTextToImageWorkflow(prompt string, opts GenerateOptions) map[string]interface{} {
	// Generate random seed if not specified
	seed := opts.Seed
	if seed < 0 {
		seed = rand.New(rand.NewSource(time.Now().UnixNano())).Int63()
	}

	workflow := map[string]interface{}{
		// Node 1: Load the diffusion model (UNET)
		"1": map[string]interface{}{
			"class_type": "UNETLoader",
			"inputs": map[string]interface{}{
				"unet_name":   opts.Model,
				"weight_dtype": "fp8_e4m3fn",
			},
		},

		// Node 2: Load the text encoder (CLIP) - Qwen specific
		"2": map[string]interface{}{
			"class_type": "CLIPLoader",
			"inputs": map[string]interface{}{
				"clip_name": opts.TextEncoder,
				"type":      "qwen_image",
			},
		},

		// Node 3: Load VAE
		"3": map[string]interface{}{
			"class_type": "VAELoader",
			"inputs": map[string]interface{}{
				"vae_name": opts.VAE,
			},
		},

		// Node 4: Encode positive prompt
		"4": map[string]interface{}{
			"class_type": "CLIPTextEncode",
			"inputs": map[string]interface{}{
				"text": prompt,
				"clip": []interface{}{"2", 0},
			},
		},

		// Node 5: Encode negative prompt
		"5": map[string]interface{}{
			"class_type": "CLIPTextEncode",
			"inputs": map[string]interface{}{
				"text": opts.NegativePrompt,
				"clip": []interface{}{"2", 0},
			},
		},

		// Node 6: Empty latent image
		"6": map[string]interface{}{
			"class_type": "EmptyLatentImage",
			"inputs": map[string]interface{}{
				"width":      opts.Width,
				"height":     opts.Height,
				"batch_size": 1,
			},
		},

		// Node 7: KSampler - the main sampling node
		"7": map[string]interface{}{
			"class_type": "KSampler",
			"inputs": map[string]interface{}{
				"model":         []interface{}{"1", 0},
				"positive":      []interface{}{"4", 0},
				"negative":      []interface{}{"5", 0},
				"latent_image":  []interface{}{"6", 0},
				"seed":          seed,
				"steps":         opts.Steps,
				"cfg":           opts.CFG,
				"sampler_name":  opts.Sampler,
				"scheduler":     opts.Scheduler,
				"denoise":       1.0,
			},
		},

		// Node 8: Decode latent to image
		"8": map[string]interface{}{
			"class_type": "VAEDecode",
			"inputs": map[string]interface{}{
				"samples": []interface{}{"7", 0},
				"vae":     []interface{}{"3", 0},
			},
		},

		// Node 9: Save image
		"9": map[string]interface{}{
			"class_type": "SaveImage",
			"inputs": map[string]interface{}{
				"images":          []interface{}{"8", 0},
				"filename_prefix": "comfyui_api",
			},
		},
	}

	return workflow
}

