package agent

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/gitopedia/researcher/internal/comfyui"
)

// PendingImagePrompt represents an image prompt waiting to be generated
type PendingImagePrompt struct {
	Topic       string
	PromptPath  string
	PromptText  string
	OutputPath  string
	ArticlePath string
	ImageType   string // "header" or "section"
}

// GenerateImages runs the image generation process for all pending prompts
// This should be called after the main research run, with Ollama stopped
func (a *Agent) GenerateImages(ctx context.Context, branchName string) error {
	log.Println("[Image Generation] Starting image generation process...")

	// Find all pending image prompts
	pending, err := a.findPendingImagePrompts(branchName)
	if err != nil {
		return fmt.Errorf("failed to find pending prompts: %w", err)
	}

	if len(pending) == 0 {
		log.Println("[Image Generation] No pending image prompts found")
		return nil
	}

	log.Printf("[Image Generation] Found %d pending prompts", len(pending))

	// Stop Ollama to free VRAM
	if err := a.stopOllama(); err != nil {
		slog.Warn("Failed to stop Ollama", "error", err)
	}

	// Start ComfyUI
	comfyClient, err := a.startComfyUI(ctx)
	if err != nil {
		return fmt.Errorf("failed to start ComfyUI: %w", err)
	}
	defer a.stopComfyUI()

	// Generate images
	generatedCount := 0
	errorCount := 0

	for _, p := range pending {
		select {
		case <-ctx.Done():
			log.Println("[Image Generation] Cancelled")
			return ctx.Err()
		default:
		}

		log.Printf("[Image Generation] Generating image for '%s' (type: %s)...", p.Topic, p.ImageType)

		// Use defaults and override size based on image type
		opts := comfyui.DefaultOptions()
		if p.ImageType == "header" {
			opts.Width = 1920
			opts.Height = 1080
		} else {
			// Section images are smaller (1024x768 landscape)
			opts.Width = 1024
			opts.Height = 768
		}
		imageData, err := comfyClient.GenerateImage(ctx, p.PromptText, &opts)
		if err != nil {
			slog.Error("Failed to generate image", "topic", p.Topic, "error", err)
			errorCount++
			continue
		}

		if err := a.saveGeneratedImage(branchName, p, imageData); err != nil {
			slog.Error("Failed to save image", "topic", p.Topic, "error", err)
			errorCount++
			continue
		}

		generatedCount++
		log.Printf("[Image Generation] Generated image for '%s' -> %s", p.Topic, p.OutputPath)

		// Insert header image reference into the markdown file
		if p.ImageType == "header" {
			if err := a.insertHeaderImageInMarkdown(branchName, p.ArticlePath, p.OutputPath); err != nil {
				slog.Error("Failed to insert header image into markdown", "article", p.Topic, "error", err)
				// Continue, as image is still generated and saved
			}
		}
	}

	log.Printf("[Image Generation] Completed: %d generated, %d errors", generatedCount, errorCount)

	// Restart Ollama
	if err := a.startOllama(); err != nil {
		slog.Warn("Failed to restart Ollama", "error", err)
	}

	return nil
}

// findPendingImagePrompts finds all image prompts that haven't been generated yet
func (a *Agent) findPendingImagePrompts(branchName string) ([]PendingImagePrompt, error) {
	var pending []PendingImagePrompt

	// List debug articles directory
	debugPath := "Compendium/_debug/articles"
	articles, err := a.gh.ListDirectory(branchName, debugPath)
	if err != nil {
		return nil, fmt.Errorf("failed to list debug articles: %w", err)
	}

	for _, articleDir := range articles {
		// Check for header_image_prompt.txt
		headerPromptPath := filepath.Join(debugPath, articleDir, "header_image_prompt.txt")
		headerPromptContent, _, err := a.gh.GetFile(branchName, headerPromptPath)
		if err == nil {
			// Extract the actual prompt (after metadata comments)
			promptText := extractPromptText(headerPromptContent)
			if promptText != "" {
				// Check if image already exists
				articlePath := filepath.Join("Compendium/_incoming", articleDir+".md")
				outputPath := filepath.Join("Compendium/_incoming", articleDir+"_header.png")

				_, _, err = a.gh.GetFile(branchName, outputPath)
				if err != nil {
					// Image doesn't exist, add to pending
					pending = append(pending, PendingImagePrompt{
						Topic:       articleDir,
						PromptPath:  headerPromptPath,
						PromptText:  promptText,
						OutputPath:  outputPath,
						ArticlePath: articlePath,
						ImageType:   "header",
					})
				}
			}
		}

		// Check for section image prompts (section_*_image_prompt.txt)
		articleDebugPath := filepath.Join(debugPath, articleDir)
		files, err := a.gh.ListDirectory(branchName, articleDebugPath)
		if err != nil {
			continue
		}

		for _, file := range files {
			if !strings.HasPrefix(file, "section_") || !strings.HasSuffix(file, "_image_prompt.txt") {
				continue
			}

			sectionPromptPath := filepath.Join(articleDebugPath, file)
			sectionPromptContent, _, err := a.gh.GetFile(branchName, sectionPromptPath)
			if err != nil {
				continue
			}

			promptText := extractPromptText(sectionPromptContent)
			if promptText == "" {
				continue
			}

			// Extract section name from filename (section_<name>_image_prompt.txt)
			sectionSlug := strings.TrimPrefix(file, "section_")
			sectionSlug = strings.TrimSuffix(sectionSlug, "_image_prompt.txt")

			// Check if image already exists
			articlePath := filepath.Join("Compendium/_incoming", articleDir+".md")
			outputPath := filepath.Join("Compendium/_incoming", articleDir+"_section_"+sectionSlug+".png")

			_, _, err = a.gh.GetFile(branchName, outputPath)
			if err != nil {
				// Image doesn't exist, add to pending
				pending = append(pending, PendingImagePrompt{
					Topic:       articleDir + " - " + sectionSlug,
					PromptPath:  sectionPromptPath,
					PromptText:  promptText,
					OutputPath:  outputPath,
					ArticlePath: articlePath,
					ImageType:   "section",
				})
			}
		}
	}

	return pending, nil
}

// extractPromptText extracts the actual prompt from the prompt file (skipping metadata comments)
func extractPromptText(content string) string {
	lines := strings.Split(content, "\n")
	var promptLines []string
	inPrompt := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if trimmed == "" && !inPrompt {
			continue
		}
		inPrompt = true
		promptLines = append(promptLines, line)
	}

	return strings.TrimSpace(strings.Join(promptLines, "\n"))
}

// saveGeneratedImage saves the generated image to the repository
func (a *Agent) saveGeneratedImage(branchName string, p PendingImagePrompt, imageData []byte) error {
	message := fmt.Sprintf("Add header image for %s", p.Topic)
	return a.gh.AddBinaryFile(branchName, p.OutputPath, message, imageData)
}

// insertHeaderImageInMarkdown inserts the header image reference into the article markdown
func (a *Agent) insertHeaderImageInMarkdown(branchName, articlePath, imagePath string) error {
	content, sha, err := a.gh.GetFile(branchName, articlePath)
	if err != nil {
		return fmt.Errorf("failed to read article file: %w", err)
	}

	// Check if image is already present
	imageFilename := filepath.Base(imagePath)
	if strings.Contains(content, fmt.Sprintf("![Header](%s)", imageFilename)) {
		log.Printf("[Header Image] Already present in %s, skipping insertion", articlePath)
		return nil
	}

	// Find the end of the frontmatter (second "---")
	lines := strings.Split(content, "\n")
	frontmatterEnd := -1
	dashCount := 0

	for i, line := range lines {
		if strings.TrimSpace(line) == "---" {
			dashCount++
			if dashCount == 2 {
				frontmatterEnd = i
				break
			}
		}
	}

	if frontmatterEnd == -1 {
		return fmt.Errorf("could not find frontmatter end in %s", articlePath)
	}

	// Build new content with image inserted after frontmatter
	var newContentBuilder strings.Builder
	for i, line := range lines {
		newContentBuilder.WriteString(line)
		newContentBuilder.WriteString("\n")
		if i == frontmatterEnd {
			// Insert image reference after the frontmatter
			newContentBuilder.WriteString(fmt.Sprintf("\n![Header](%s)\n", imageFilename))
		}
	}

	newContent := strings.TrimSuffix(newContentBuilder.String(), "\n")
	return a.gh.UpdateFile(branchName, articlePath, fmt.Sprintf("Add header image to %s", filepath.Base(articlePath)), newContent, sha)
}

// BackfillImagePrompts generates image prompts for existing articles that don't have them
func (a *Agent) BackfillImagePrompts(ctx context.Context, branchName string) error {
	log.Println("[Backfill] Starting image prompt backfill...")

	// List incoming articles
	incomingPath := "Compendium/_incoming"
	files, err := a.gh.ListDirectory(branchName, incomingPath)
	if err != nil {
		return fmt.Errorf("failed to list incoming articles: %w", err)
	}

	backfilledCount := 0
	for _, file := range files {
		if !strings.HasSuffix(file, ".md") {
			continue
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		articleName := strings.TrimSuffix(file, ".md")

		// Check if prompt already exists
		promptPath := filepath.Join("Compendium/_debug/articles", articleName, "header_image_prompt.txt")
		if _, _, err := a.gh.GetFile(branchName, promptPath); err == nil {
			log.Printf("[Backfill] Prompt already exists for %s, skipping", articleName)
			continue
		}

		// Get article content to determine category
		articlePath := filepath.Join(incomingPath, file)
		content, _, err := a.gh.GetFile(branchName, articlePath)
		if err != nil {
			slog.Warn("Failed to read article", "file", file, "error", err)
			continue
		}

		// Extract category from frontmatter
		category, subcategory := extractCategoryFromFrontmatter(content)

		log.Printf("[Backfill] Generating prompt for %s (category: %s/%s)...", articleName, category, subcategory)

		if err := a.generateHeaderImagePrompt(ctx, articleName, branchName, category, subcategory); err != nil {
			slog.Error("Failed to generate prompt", "article", articleName, "error", err)
			continue
		}

		backfilledCount++
	}

	log.Printf("[Backfill] Completed: %d prompts generated", backfilledCount)
	return nil
}

// extractCategoryFromFrontmatter extracts category info from article frontmatter
func extractCategoryFromFrontmatter(content string) (category, subcategory string) {
	lines := strings.Split(content, "\n")
	inFrontmatter := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			if !inFrontmatter {
				inFrontmatter = true
				continue
			}
			break
		}
		if !inFrontmatter {
			continue
		}

		if strings.HasPrefix(trimmed, "category:") {
			category = strings.TrimSpace(strings.TrimPrefix(trimmed, "category:"))
			category = strings.Trim(category, "\"'")
		}
		if strings.HasPrefix(trimmed, "subcategory:") {
			subcategory = strings.TrimSpace(strings.TrimPrefix(trimmed, "subcategory:"))
			subcategory = strings.Trim(subcategory, "\"'")
		}
	}

	// Default category if not found
	if category == "" {
		category = "science"
	}

	return category, subcategory
}

// runDockerComposeCmd runs a docker-compose command in the configured directory
func (a *Agent) runDockerComposeCmd(cmdStr string) error {
	if cmdStr == "" {
		return nil
	}

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

// stopOllama stops the Ollama service to free VRAM
func (a *Agent) stopOllama() error {
	log.Println("[VRAM] Stopping Ollama...")

	stopCmd := os.Getenv("OLLAMA_STOP_CMD")
	if stopCmd == "" {
		stopCmd = "docker compose stop ollama"
	}

	if err := a.runDockerComposeCmd(stopCmd); err != nil {
		// Not an error if it wasn't running
		log.Println("[VRAM] Ollama stop command completed (may not have been running)")
	}

	// Wait for VRAM to be freed
	time.Sleep(2 * time.Second)
	return nil
}

// startOllama starts the Ollama service
func (a *Agent) startOllama() error {
	log.Println("[VRAM] Starting Ollama...")

	startCmd := os.Getenv("OLLAMA_START_CMD")
	if startCmd == "" {
		startCmd = "docker compose start ollama"
	}

	if err := a.runDockerComposeCmd(startCmd); err != nil {
		return fmt.Errorf("failed to start Ollama: %w", err)
	}

	// Wait for Ollama to be ready
	time.Sleep(5 * time.Second)
	return nil
}

// startComfyUI starts the ComfyUI service and returns a client
func (a *Agent) startComfyUI(ctx context.Context) (*comfyui.Client, error) {
	log.Println("[VRAM] Starting ComfyUI...")

	// ComfyUI URL from environment or default
	comfyURL := os.Getenv("COMFYUI_URL")
	if comfyURL == "" {
		comfyURL = "http://localhost:8188"
	}

	client := comfyui.NewClient(comfyURL)

	// Check if ComfyUI is already running
	if client.IsHealthy(ctx) {
		log.Println("[ComfyUI] Already running")
		return client, nil
	}

	// Try to start ComfyUI using configured command
	startCmd := os.Getenv("COMFYUI_START_CMD")
	if startCmd == "" {
		startCmd = "docker compose up -d comfyui"
	}

	log.Printf("[ComfyUI] Starting with command: %s", startCmd)
	if err := a.runDockerComposeCmd(startCmd); err != nil {
		return nil, fmt.Errorf("failed to start ComfyUI: %w", err)
	}

	// Wait for ComfyUI to be ready (up to 3 minutes for model loading)
	log.Println("[ComfyUI] Waiting for ComfyUI to be ready...")
	for i := 0; i < 90; i++ {
		if client.IsHealthy(ctx) {
			log.Println("[ComfyUI] Ready")
			return client, nil
		}
		time.Sleep(2 * time.Second)
	}

	return nil, fmt.Errorf("ComfyUI did not become ready within timeout")
}

// stopComfyUI stops the ComfyUI service
func (a *Agent) stopComfyUI() {
	log.Println("[VRAM] Stopping ComfyUI...")

	stopCmd := os.Getenv("COMFYUI_STOP_CMD")
	if stopCmd == "" {
		stopCmd = "docker compose stop comfyui"
	}

	if err := a.runDockerComposeCmd(stopCmd); err != nil {
		slog.Warn("Failed to stop ComfyUI", "error", err)
	}
}

