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

// ImageGenConfig holds configuration for image generation
type ImageGenConfig struct {
	ComfyUIURL    string
	HeaderWidth   int
	HeaderHeight  int
	OllamaService string // Service name or path for Ollama control
}

// DefaultImageGenConfig returns default configuration
func DefaultImageGenConfig() ImageGenConfig {
	return ImageGenConfig{
		ComfyUIURL:    getEnvOrDefault("COMFYUI_URL", "http://localhost:8188"),
		HeaderWidth:   getEnvInt("IMAGE_HEADER_WIDTH", 1920),
		HeaderHeight:  getEnvInt("IMAGE_HEADER_HEIGHT", 1080),
		OllamaService: getEnvOrDefault("OLLAMA_SERVICE", "ollama"),
	}
}

// PendingImagePrompt represents a pending image generation task
type PendingImagePrompt struct {
	Slug       string
	Topic      string
	PromptPath string
	Prompt     string
	OutputPath string
}

// GenerateImages runs the image generation process for all pending prompts
// This manages the Ollama/ComfyUI lifecycle to handle VRAM constraints
func (a *Agent) GenerateImages(ctx context.Context, branchName string) error {
	config := DefaultImageGenConfig()

	log.Println("=== Image Generation Phase ===")

	// Find all pending image prompts
	pendingPrompts, err := a.findPendingImagePrompts(branchName)
	if err != nil {
		return fmt.Errorf("failed to find pending prompts: %w", err)
	}

	if len(pendingPrompts) == 0 {
		log.Println("No pending image prompts found")
		return nil
	}

	log.Printf("Found %d pending image prompts", len(pendingPrompts))

	// Step 1: Shutdown Ollama to free VRAM
	log.Println("Shutting down Ollama to free VRAM...")
	if err := shutdownOllama(config.OllamaService); err != nil {
		slog.Warn("Failed to shutdown Ollama (may not be running)", "error", err)
	}

	// Step 2: Start ComfyUI
	log.Println("Starting ComfyUI...")
	comfyStarted := false
	if err := startComfyUI(ctx, config.ComfyUIURL); err != nil {
		slog.Warn("Failed to start ComfyUI", "error", err)
	} else {
		comfyStarted = true
	}

	// Step 3: Generate images
	var generatedCount, errorCount int
	if comfyStarted {
		client := comfyui.NewClient(config.ComfyUIURL)

		for _, pending := range pendingPrompts {
			log.Printf("Generating image for '%s'...", pending.Topic)

			opts := comfyui.DefaultOptions()
			opts.Width = config.HeaderWidth
			opts.Height = config.HeaderHeight

			imageData, err := client.GenerateImage(ctx, pending.Prompt, &opts)
			if err != nil {
				slog.Error("Failed to generate image", "topic", pending.Topic, "error", err)
				errorCount++
				continue
			}

			// Save image to the repository
			if err := a.saveGeneratedImage(branchName, pending, imageData); err != nil {
				slog.Error("Failed to save image", "topic", pending.Topic, "error", err)
				errorCount++
				continue
			}

			generatedCount++
			log.Printf("Generated image for '%s' -> %s", pending.Topic, pending.OutputPath)
		}
	} else {
		log.Println("ComfyUI not available, skipping image generation")
		errorCount = len(pendingPrompts)
	}

	// Step 4: Shutdown ComfyUI
	log.Println("Shutting down ComfyUI...")
	if err := shutdownComfyUI(); err != nil {
		slog.Warn("Failed to shutdown ComfyUI", "error", err)
	}

	// Step 5: Restart Ollama
	log.Println("Restarting Ollama...")
	if err := restartOllama(config.OllamaService); err != nil {
		slog.Warn("Failed to restart Ollama", "error", err)
	}

	log.Printf("Image generation complete: %d generated, %d errors", generatedCount, errorCount)
	return nil
}

// findPendingImagePrompts scans the debug folders for pending image prompts
func (a *Agent) findPendingImagePrompts(branchName string) ([]PendingImagePrompt, error) {
	var pending []PendingImagePrompt

	// List directories in Compendium/_debug/articles/
	debugPath := "Compendium/_debug/articles"

	// Try to list the debug directory
	entries, err := a.gh.ListDirectory(branchName, debugPath)
	if err != nil {
		// Directory might not exist yet
		return pending, nil
	}

	for _, entry := range entries {
		if !entry.IsDir {
			continue
		}

		slug := entry.Name
		promptPath := fmt.Sprintf("%s/%s/header_image_prompt.txt", debugPath, slug)
		outputPath := fmt.Sprintf("Compendium/_incoming/%s_header.png", slug)

		// Check if prompt file exists
		promptContent, _, err := a.gh.GetFile(branchName, promptPath)
		if err != nil {
			continue // No prompt file
		}

		// Check if image already exists
		_, _, err = a.gh.GetFile(branchName, outputPath)
		if err == nil {
			// Image already exists, skip
			continue
		}

		// Extract the actual prompt (skip header comments)
		prompt := extractPromptFromFile(promptContent)
		if prompt == "" {
			slog.Warn("Empty prompt file", "path", promptPath)
			continue
		}

		// Extract topic from prompt file header
		topic := extractTopicFromPromptFile(promptContent)
		if topic == "" {
			topic = slug
		}

		pending = append(pending, PendingImagePrompt{
			Slug:       slug,
			Topic:      topic,
			PromptPath: promptPath,
			Prompt:     prompt,
			OutputPath: outputPath,
		})
	}

	return pending, nil
}

// saveGeneratedImage saves the generated image to the repository
func (a *Agent) saveGeneratedImage(branchName string, pending PendingImagePrompt, imageData []byte) error {
	// Create or update the image file
	// Note: This requires binary file support in the repository manager
	// For now, we'll save it locally and commit

	// Create a temporary file
	tmpDir := os.TempDir()
	tmpPath := filepath.Join(tmpDir, fmt.Sprintf("%s_header.png", pending.Slug))

	if err := os.WriteFile(tmpPath, imageData, 0644); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}
	defer os.Remove(tmpPath)

	// Use git commands to add the binary file
	// This is a workaround since the GitHub API client may not handle binary files well
	if err := a.gh.AddBinaryFile(branchName, pending.OutputPath, tmpPath, fmt.Sprintf("Add header image for %s", pending.Topic)); err != nil {
		return fmt.Errorf("failed to add image to repository: %w", err)
	}

	return nil
}

// extractPromptFromFile extracts the actual prompt text from a prompt file
// (skipping header comments that start with #)
func extractPromptFromFile(content string) string {
	lines := strings.Split(content, "\n")
	var promptLines []string
	inPrompt := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Skip empty lines at the start
		if !inPrompt && trimmed == "" {
			continue
		}
		// Skip header comments
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		// We've reached the actual prompt
		inPrompt = true
		promptLines = append(promptLines, line)
	}

	return strings.TrimSpace(strings.Join(promptLines, "\n"))
}

// extractTopicFromPromptFile extracts the topic from the prompt file header
func extractTopicFromPromptFile(content string) string {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# Header Image Prompt for: ") {
			return strings.TrimPrefix(trimmed, "# Header Image Prompt for: ")
		}
	}
	return ""
}

// shutdownOllama stops the Ollama service to free VRAM
func shutdownOllama(service string) error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "windows":
		// On Windows, try to stop via taskkill
		cmd = exec.Command("taskkill", "/F", "/IM", "ollama.exe")
	case "darwin":
		// On macOS, try launchctl or pkill
		cmd = exec.Command("pkill", "-f", "ollama")
	default:
		// On Linux, try systemctl or pkill
		cmd = exec.Command("systemctl", "stop", service)
		if err := cmd.Run(); err != nil {
			// Fall back to pkill
			cmd = exec.Command("pkill", "-f", "ollama")
		}
	}

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to stop ollama: %w", err)
	}

	// Wait a moment for VRAM to be freed
	time.Sleep(2 * time.Second)
	return nil
}

// startComfyUI starts the ComfyUI server and waits for it to be ready
func startComfyUI(ctx context.Context, baseURL string) error {
	client := comfyui.NewClient(baseURL)

	// First check if it's already running
	if client.IsHealthy(ctx) {
		log.Println("ComfyUI is already running")
		return nil
	}

	// Try to start ComfyUI
	comfyPath := os.Getenv("COMFYUI_PATH")
	if comfyPath == "" {
		// Try common locations
		switch runtime.GOOS {
		case "windows":
			comfyPath = filepath.Join(os.Getenv("USERPROFILE"), "ComfyUI", "main.py")
		default:
			comfyPath = filepath.Join(os.Getenv("HOME"), "ComfyUI", "main.py")
		}
	}

	if _, err := os.Stat(comfyPath); os.IsNotExist(err) {
		return fmt.Errorf("ComfyUI not found at %s, set COMFYUI_PATH environment variable", comfyPath)
	}

	// Start ComfyUI in background
	pythonCmd := getEnvOrDefault("PYTHON_CMD", "python")
	cmd := exec.Command(pythonCmd, comfyPath, "--listen", "0.0.0.0")
	cmd.Dir = filepath.Dir(comfyPath)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start ComfyUI: %w", err)
	}

	log.Printf("Started ComfyUI (PID: %d), waiting for it to be ready...", cmd.Process.Pid)

	// Wait for ComfyUI to become healthy
	timeout := time.After(2 * time.Minute)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout:
			return fmt.Errorf("timeout waiting for ComfyUI to start")
		case <-ticker.C:
			if client.IsHealthy(ctx) {
				log.Println("ComfyUI is ready")
				return nil
			}
		}
	}
}

// shutdownComfyUI stops the ComfyUI server
func shutdownComfyUI() error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "windows":
		// On Windows, find and kill the Python process running ComfyUI
		cmd = exec.Command("taskkill", "/F", "/IM", "python.exe", "/FI", "WINDOWTITLE eq ComfyUI*")
		if err := cmd.Run(); err != nil {
			// Try a more aggressive approach
			cmd = exec.Command("powershell", "-Command",
				"Get-Process python | Where-Object {$_.MainWindowTitle -like '*ComfyUI*'} | Stop-Process -Force")
		}
	default:
		// On Unix-like systems
		cmd = exec.Command("pkill", "-f", "ComfyUI")
	}

	if err := cmd.Run(); err != nil {
		// Not fatal - ComfyUI might not have been running
		return fmt.Errorf("failed to stop ComfyUI: %w", err)
	}

	time.Sleep(2 * time.Second)
	return nil
}

// restartOllama restarts the Ollama service
func restartOllama(service string) error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "windows":
		// On Windows, start Ollama
		ollamaPath := getEnvOrDefault("OLLAMA_PATH", "ollama")
		cmd = exec.Command(ollamaPath, "serve")
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("failed to start ollama: %w", err)
		}
		log.Printf("Started Ollama (PID: %d)", cmd.Process.Pid)
	case "darwin":
		// On macOS, start via open or brew services
		cmd = exec.Command("ollama", "serve")
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("failed to start ollama: %w", err)
		}
	default:
		// On Linux, use systemctl
		cmd = exec.Command("systemctl", "start", service)
		if err := cmd.Run(); err != nil {
			// Fall back to direct start
			cmd = exec.Command("ollama", "serve")
			if err := cmd.Start(); err != nil {
				return fmt.Errorf("failed to start ollama: %w", err)
			}
		}
	}

	// Wait for Ollama to be ready
	time.Sleep(5 * time.Second)
	return nil
}

// GenerateImagesForBranch is the main entry point for image generation
// It can be called directly via CLI flag or as part of the finalization step
func (a *Agent) GenerateImagesForBranch(ctx context.Context, branchName string) error {
	return a.GenerateImages(ctx, branchName)
}

// RunImageGenerationOnly runs only the image generation phase
// This is used when the --generate-images flag is passed
func (a *Agent) RunImageGenerationOnly(ctx context.Context) error {
	log.Println("Running image generation only mode...")

	// Get the current branch
	currentBranch, err := a.gh.GetCurrentBranch()
	if err != nil {
		return fmt.Errorf("failed to get current branch: %w", err)
	}

	if currentBranch == "main" {
		return fmt.Errorf("cannot run image generation on main branch, switch to a research branch first")
	}

	return a.GenerateImagesForBranch(ctx, currentBranch)
}

// BackfillImagePrompts generates image prompts for existing articles that don't have them
// This scans articles in Compendium/_incoming/ and creates prompts for any that are missing
func (a *Agent) BackfillImagePrompts(ctx context.Context, branchName string) error {
	log.Println("=== Backfilling Image Prompts for Existing Articles ===")

	// Find all markdown files in Compendium/_incoming/
	articles, err := a.findArticlesNeedingPrompts(branchName)
	if err != nil {
		return fmt.Errorf("failed to find articles: %w", err)
	}

	if len(articles) == 0 {
		log.Println("No articles need image prompts")
		return nil
	}

	log.Printf("Found %d articles needing image prompts", len(articles))

	var generated, failed int
	for _, article := range articles {
		log.Printf("Generating prompt for '%s'...", article.Title)

		// Extract category from tags or use default
		category, subcategory := extractCategoryFromTags(article.Tags)

		if err := a.generateHeaderImagePrompt(ctx, article.Title, branchName, category, subcategory); err != nil {
			slog.Error("Failed to generate prompt", "article", article.Title, "error", err)
			failed++
			continue
		}

		generated++
		log.Printf("Generated prompt for '%s' (%s > %s)", article.Title, category, subcategory)
	}

	log.Printf("Backfill complete: %d generated, %d failed", generated, failed)
	return nil
}

// ArticleInfo contains basic info about an article for backfill processing
type ArticleInfo struct {
	Slug  string
	Title string
	Tags  []string
	Path  string
}

// findArticlesNeedingPrompts finds articles in _incoming that don't have image prompts
func (a *Agent) findArticlesNeedingPrompts(branchName string) ([]ArticleInfo, error) {
	var articles []ArticleInfo

	// List files in Compendium/_incoming/
	incomingPath := "Compendium/_incoming"
	files, err := a.gh.ListFilesInBranch(branchName, incomingPath)
	if err != nil {
		return nil, fmt.Errorf("failed to list incoming files: %w", err)
	}

	for _, file := range files {
		// Only process markdown files (not directories like sources/)
		if !strings.HasSuffix(file, ".md") {
			continue
		}

		// Extract slug from filename
		filename := strings.TrimPrefix(file, incomingPath+"/")
		if strings.Contains(filename, "/") {
			continue // Skip files in subdirectories
		}
		slug := strings.TrimSuffix(filename, ".md")

		// Check if prompt already exists
		promptPath := fmt.Sprintf("Compendium/_debug/articles/%s/header_image_prompt.txt", slug)
		_, _, err := a.gh.GetFile(branchName, promptPath)
		if err == nil {
			// Prompt already exists, skip
			log.Printf("Skipping '%s' - prompt already exists", slug)
			continue
		}

		// Load article to get title and tags
		content, _, err := a.gh.GetFile(branchName, file)
		if err != nil {
			slog.Warn("Failed to read article", "file", file, "error", err)
			continue
		}

		title, tags := parseArticleFrontmatter(content)
		if title == "" {
			title = slug // Fallback to slug
		}

		articles = append(articles, ArticleInfo{
			Slug:  slug,
			Title: title,
			Tags:  tags,
			Path:  file,
		})
	}

	return articles, nil
}

// parseArticleFrontmatter extracts title and tags from article frontmatter
func parseArticleFrontmatter(content string) (string, []string) {
	lines := strings.Split(content, "\n")
	inFrontmatter := false
	var title string
	var tags []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if trimmed == "---" {
			if !inFrontmatter {
				inFrontmatter = true
				continue
			} else {
				break // End of frontmatter
			}
		}

		if !inFrontmatter {
			continue
		}

		// Parse title
		if strings.HasPrefix(trimmed, "title:") {
			title = strings.TrimPrefix(trimmed, "title:")
			title = strings.TrimSpace(title)
			title = strings.Trim(title, "\"'")
		}

		// Parse tags
		if strings.HasPrefix(trimmed, "tags:") {
			tagStr := strings.TrimPrefix(trimmed, "tags:")
			tagStr = strings.TrimSpace(tagStr)
			// Parse JSON-like array: ["tag1", "tag2"]
			tagStr = strings.Trim(tagStr, "[]")
			for _, tag := range strings.Split(tagStr, ",") {
				tag = strings.TrimSpace(tag)
				tag = strings.Trim(tag, "\"'")
				if tag != "" {
					tags = append(tags, tag)
				}
			}
		}
	}

	return title, tags
}

// extractCategoryFromTags attempts to extract category/subcategory from article tags
func extractCategoryFromTags(tags []string) (string, string) {
	// Map common tag prefixes to categories
	categoryMap := map[string]string{
		"quantum":     "Science",
		"physics":     "Science",
		"biology":     "Science",
		"chemistry":   "Science",
		"astronomy":   "Science",
		"mathematics": "Science",
		"history":     "History",
		"ancient":     "History",
		"medieval":    "History",
		"modern":      "History",
		"person":      "People",
		"people":      "People",
		"art":         "Arts",
		"music":       "Arts",
		"literature":  "Arts",
		"technology":  "Technology",
		"engineering": "Technology",
		"philosophy":  "Philosophy",
		"culture":     "Culture",
		"religion":    "Culture",
		"geography":   "Geography",
	}

	subcategoryMap := map[string]string{
		"quantum-mechanics":   "Physics",
		"quantum-physics":     "Physics",
		"quantum-entanglement": "Physics",
		"wave-function":       "Physics",
		"superposition":       "Physics",
		"thermodynamics":      "Physics",
		"electromagnetism":    "Physics",
		"biology":             "Biology",
		"genetics":            "Biology",
		"chemistry":           "Chemistry",
		"organic-chemistry":   "Chemistry",
		"astronomy":           "Astronomy",
		"cosmology":           "Astronomy",
	}

	category := "Science" // Default
	subcategory := ""

	for _, tag := range tags {
		// Remove topic: prefix if present
		tag = strings.TrimPrefix(tag, "topic:")
		tagLower := strings.ToLower(tag)

		// Check for subcategory first (more specific)
		if sub, ok := subcategoryMap[tagLower]; ok {
			subcategory = sub
		}

		// Check for category
		for prefix, cat := range categoryMap {
			if strings.Contains(tagLower, prefix) {
				category = cat
				break
			}
		}
	}

	// If we identified a subcategory, make sure we have the right parent category
	if subcategory != "" {
		switch subcategory {
		case "Physics", "Biology", "Chemistry", "Astronomy":
			category = "Science"
		}
	}

	return category, subcategory
}

