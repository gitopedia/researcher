// Command comfyui-test tests ComfyUI image generation
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/gitopedia/researcher/internal/comfyui"
)

func main() {
	// Flags
	url := flag.String("url", "http://localhost:8188", "ComfyUI API URL")
	prompt := flag.String("prompt", "a beautiful sunset over mountains", "Image generation prompt")
	output := flag.String("output", "output.png", "Output file path")
	width := flag.Int("width", 1024, "Image width")
	height := flag.Int("height", 1024, "Image height")
	steps := flag.Int("steps", 20, "Number of sampling steps")
	cfg := flag.Float64("cfg", 4.5, "CFG scale")
	timeout := flag.Duration("timeout", 15*time.Minute, "Generation timeout")
	flag.Parse()

	// Create client
	client := comfyui.NewClient(*url)

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	// Check health
	fmt.Println("Checking ComfyUI...")
	if !client.IsHealthy(ctx) {
		fmt.Println("✗ ComfyUI is not responding at", *url)
		os.Exit(1)
	}
	fmt.Println("✓ ComfyUI is ready")

	// Generate image
	fmt.Printf("Generating image for prompt: %s\n", *prompt)
	fmt.Printf("  Size: %dx%d, Steps: %d, CFG: %.1f\n", *width, *height, *steps, *cfg)

	opts := comfyui.DefaultOptions()
	opts.Width = *width
	opts.Height = *height
	opts.Steps = *steps
	opts.CFG = *cfg

	start := time.Now()
	imageData, err := client.GenerateImage(ctx, *prompt, &opts)
	if err != nil {
		fmt.Printf("✗ Failed to generate image: %v\n", err)
		os.Exit(1)
	}
	elapsed := time.Since(start)

	// Save image
	if err := os.WriteFile(*output, imageData, 0644); err != nil {
		fmt.Printf("✗ Failed to save image: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✓ Image generated in %s\n", elapsed.Round(time.Second))
	fmt.Printf("✓ Saved to %s (%d bytes)\n", *output, len(imageData))
}




