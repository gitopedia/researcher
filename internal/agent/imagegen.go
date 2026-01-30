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
	"github.com/gitopedia/researcher/internal/llm"
	"github.com/gitopedia/researcher/internal/styles"
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

	// Find all pending article image prompts
	pending, err := a.findPendingImagePrompts(branchName)
	if err != nil {
		return fmt.Errorf("failed to find pending prompts: %w", err)
	}

	// Find pending index image prompts (for domain/category/topic headers)
	pendingIndexes, err := a.findPendingIndexImagePrompts(branchName)
	if err != nil {
		slog.Warn("Failed to find pending index prompts", "error", err)
		pendingIndexes = nil
	}

	// Generate prompts for indexes that don't have them yet
	if len(pendingIndexes) > 0 {
		log.Printf("[Image Generation] Found %d indexes needing header images", len(pendingIndexes))
		for i := range pendingIndexes {
			p := &pendingIndexes[i]
			// Check if prompt already exists
			if _, _, err := a.gh.GetFile(branchName, p.PromptPath); err != nil {
				// Prompt doesn't exist, generate it
				if err := a.generateIndexImagePrompt(ctx, *p, branchName); err != nil {
					slog.Warn("Failed to generate index prompt", "index", p.Name, "error", err)
					continue
				}
			}
			// Read the prompt text
			promptContent, _, err := a.gh.GetFile(branchName, p.PromptPath)
			if err == nil {
				promptText := extractPromptText(promptContent)
				if promptText != "" {
					// Add to pending list as a regular image prompt
					pending = append(pending, PendingImagePrompt{
						Topic:       p.Name + " (" + p.IndexType + " index)",
						PromptPath:  p.PromptPath,
						PromptText:  promptText,
						OutputPath:  p.OutputPath,
						ArticlePath: p.IndexPath,
						ImageType:   "index_header",
					})
				}
			}
		}
	}

	if len(pending) == 0 {
		log.Println("[Image Generation] No pending image prompts found")
		return nil
	}

	log.Printf("[Image Generation] Found %d total pending prompts", len(pending))

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
		if p.ImageType == "header" || p.ImageType == "index_header" {
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
		if p.ImageType == "header" || p.ImageType == "index_header" {
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
		// If the directory doesn't exist, just return empty list (no article prompts pending)
		log.Printf("[Image Generation] No article prompts directory found, skipping article images")
		return pending, nil
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

// PendingIndexImagePrompt represents an index image prompt waiting to be generated
type PendingIndexImagePrompt struct {
	IndexType   string // "domain", "category", or "topic"
	Name        string // Display name (e.g., "Science", "Physics", "Quantum Mechanics")
	Slug        string // URL slug (e.g., "science", "physics", "quantum-mechanics")
	Domain      string // Parent domain (empty for domain-level)
	DomainSlug  string
	Category    string // Parent category (empty for domain/category-level)
	CategorySlug string
	ChildItems  []string // Categories/topics/articles contained
	PromptPath  string   // Path to save the prompt
	OutputPath  string   // Path for the generated image
	IndexPath   string   // Path to the index.md file
}

// findPendingIndexImagePrompts scans the Compendium directory for index files missing header images
func (a *Agent) findPendingIndexImagePrompts(branchName string) ([]PendingIndexImagePrompt, error) {
	var pending []PendingIndexImagePrompt

	// List top-level directories in Compendium (domains)
	compendiumPath := "Compendium"
	domains, err := a.gh.ListDirectory(branchName, compendiumPath)
	if err != nil {
		return nil, fmt.Errorf("failed to list Compendium directory: %w", err)
	}

	for _, domainDir := range domains {
		// Skip special directories
		if strings.HasPrefix(domainDir, "_") || domainDir == "index.md" {
			continue
		}

		domainPath := filepath.Join(compendiumPath, domainDir)
		domainIndexPath := filepath.Join(domainPath, "index.md")

		// Check if domain index exists
		domainIndexContent, _, err := a.gh.GetFile(branchName, domainIndexPath)
		if err != nil {
			continue // No index file for this domain
		}

		// Extract domain info from frontmatter
		domainName := extractFrontmatterValue(domainIndexContent, "title")
		if domainName == "" {
			domainName = domainDir
		}

		// Check if domain header image exists
		domainImagePath := filepath.Join(domainPath, "img", domainDir+"_header.png")
		if _, _, err := a.gh.GetFile(branchName, domainImagePath); err != nil {
			// Image doesn't exist - get child categories
			categories, _ := a.gh.ListDirectory(branchName, domainPath)
			var childItems []string
			for _, cat := range categories {
				if !strings.HasPrefix(cat, "_") && cat != "index.md" && cat != "img" {
					childItems = append(childItems, cat)
				}
			}

			pending = append(pending, PendingIndexImagePrompt{
				IndexType:  "domain",
				Name:       domainName,
				Slug:       domainDir,
				ChildItems: childItems,
				PromptPath: filepath.Join("Compendium/_debug/indexes", domainDir, "header_image_prompt.txt"),
				OutputPath: domainImagePath,
				IndexPath:  domainIndexPath,
			})
		}

		// Now scan categories within this domain
		categories, err := a.gh.ListDirectory(branchName, domainPath)
		if err != nil {
			continue
		}

		for _, categoryDir := range categories {
			if strings.HasPrefix(categoryDir, "_") || categoryDir == "index.md" || categoryDir == "img" {
				continue
			}

			categoryPath := filepath.Join(domainPath, categoryDir)
			categoryIndexPath := filepath.Join(categoryPath, "index.md")

			// Check if category index exists
			categoryIndexContent, _, err := a.gh.GetFile(branchName, categoryIndexPath)
			if err != nil {
				continue
			}

			categoryName := extractFrontmatterValue(categoryIndexContent, "title")
			if categoryName == "" {
				categoryName = categoryDir
			}

			// Check if category header image exists
			categoryImagePath := filepath.Join(categoryPath, "img", categoryDir+"_header.png")
			if _, _, err := a.gh.GetFile(branchName, categoryImagePath); err != nil {
				// Image doesn't exist - get child topics
				topics, _ := a.gh.ListDirectory(branchName, categoryPath)
				var childItems []string
				for _, topic := range topics {
					if !strings.HasPrefix(topic, "_") && topic != "index.md" && topic != "img" {
						childItems = append(childItems, topic)
					}
				}

				pending = append(pending, PendingIndexImagePrompt{
					IndexType:    "category",
					Name:         categoryName,
					Slug:         categoryDir,
					Domain:       domainName,
					DomainSlug:   domainDir,
					ChildItems:   childItems,
					PromptPath:   filepath.Join("Compendium/_debug/indexes", domainDir, categoryDir, "header_image_prompt.txt"),
					OutputPath:   categoryImagePath,
					IndexPath:    categoryIndexPath,
				})
			}

			// Now scan topics within this category
			topics, err := a.gh.ListDirectory(branchName, categoryPath)
			if err != nil {
				continue
			}

			for _, topicDir := range topics {
				if strings.HasPrefix(topicDir, "_") || topicDir == "index.md" || topicDir == "img" {
					continue
				}

				topicPath := filepath.Join(categoryPath, topicDir)
				topicIndexPath := filepath.Join(topicPath, "index.md")

				// Check if topic index exists
				topicIndexContent, _, err := a.gh.GetFile(branchName, topicIndexPath)
				if err != nil {
					continue
				}

				topicName := extractFrontmatterValue(topicIndexContent, "title")
				if topicName == "" {
					topicName = topicDir
				}

				// Check if topic header image exists
				topicImagePath := filepath.Join(topicPath, "img", topicDir+"_header.png")
				if _, _, err := a.gh.GetFile(branchName, topicImagePath); err != nil {
					// Image doesn't exist - get child articles
					articles, _ := a.gh.ListDirectory(branchName, topicPath)
					var childItems []string
					for _, article := range articles {
						if strings.HasSuffix(article, ".md") && article != "index.md" {
							childItems = append(childItems, strings.TrimSuffix(article, ".md"))
						}
					}

					pending = append(pending, PendingIndexImagePrompt{
						IndexType:    "topic",
						Name:         topicName,
						Slug:         topicDir,
						Domain:       domainName,
						DomainSlug:   domainDir,
						Category:     categoryName,
						CategorySlug: categoryDir,
						ChildItems:   childItems,
						PromptPath:   filepath.Join("Compendium/_debug/indexes", domainDir, categoryDir, topicDir, "header_image_prompt.txt"),
						OutputPath:   topicImagePath,
						IndexPath:    topicIndexPath,
					})
				}
			}
		}
	}

	return pending, nil
}

// extractFrontmatterValue extracts a value from YAML frontmatter
func extractFrontmatterValue(content, key string) string {
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

		if strings.HasPrefix(trimmed, key+":") {
			value := strings.TrimSpace(strings.TrimPrefix(trimmed, key+":"))
			value = strings.Trim(value, "\"'")
			return value
		}
	}

	return ""
}

// generateIndexImagePrompt generates an image prompt for an index header and saves it to the debug folder
func (a *Agent) generateIndexImagePrompt(ctx context.Context, p PendingIndexImagePrompt, branchName string) error {
	log.Printf("[Index Image Prompt] Generating prompt for %s '%s'", p.IndexType, p.Name)

	// Get config directory path
	configDir := getEnvOrDefault("CONFIG_DIR", "config")

	// Load style configuration
	styleMgr := styles.NewManager(configDir)
	if err := styleMgr.Load(); err != nil {
		return fmt.Errorf("failed to load style config: %w", err)
	}

	// Determine image type based on index type
	imageType := p.IndexType + "_header" // "domain_header", "category_header", or "topic_header"

	// Resolve category-based configuration (for styles and colors)
	domainLower := strings.ToLower(p.DomainSlug)
	categoryLower := strings.ToLower(p.CategorySlug)
	if domainLower == "" {
		domainLower = strings.ToLower(p.Slug) // For domain level, use the slug itself
	}

	resolved := styleMgr.ResolveAll(imageType, domainLower, categoryLower)

	// Select 1-2 random artistic styles from config
	numStyles := 1
	if len(resolved.ArtisticStyles) > 2 {
		numStyles = 2
	}
	selectedStyles := styleMgr.SelectRandomStyles(resolved.ArtisticStyles, numStyles)

	// Select a random color mood from config
	colorMood := styleMgr.SelectRandomColorMood(resolved.ColorMoods)

	// Generate the image prompt using LLM
	req := llm.IndexImagePromptRequest{
		IndexType:      p.IndexType,
		Name:           p.Name,
		Domain:         p.Domain,
		Category:       p.Category,
		ChildItems:     p.ChildItems,
		ColorMood:      colorMood,
		ArtisticStyles: selectedStyles,
	}

	result, err := a.llm.GenerateIndexImagePrompt(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to generate index image prompt: %w", err)
	}

	// Create debug directory path
	debugDir := filepath.Dir(p.PromptPath)

	// Build prompt content with metadata
	promptContent := fmt.Sprintf("# Index Header Image Prompt for: %s\n", p.Name)
	promptContent += fmt.Sprintf("# Index Type: %s\n", p.IndexType)
	if p.Domain != "" {
		promptContent += fmt.Sprintf("# Domain: %s\n", p.Domain)
	}
	if p.Category != "" {
		promptContent += fmt.Sprintf("# Category: %s\n", p.Category)
	}
	promptContent += fmt.Sprintf("# Child Items: %s\n", strings.Join(p.ChildItems, ", "))
	promptContent += fmt.Sprintf("# Style: %s\n", strings.Join(selectedStyles, ", "))
	promptContent += fmt.Sprintf("# Color Mood: %s\n", colorMood)
	promptContent += fmt.Sprintf("# Model: %s\n", result.Model)
	promptContent += "\n"
	promptContent += result.Prompt

	// Save the prompt to the debug folder
	if err := a.gh.CreateFile(branchName, p.PromptPath, fmt.Sprintf("Add index image prompt for %s", p.Name), promptContent); err != nil {
		// Try creating parent directories first
		slog.Warn("Failed to save prompt, trying with parent dirs", "path", p.PromptPath, "error", err)
	}

	log.Printf("[Index Image Prompt] Generated prompt for %s '%s' (%d chars)", p.IndexType, p.Name, len(result.Prompt))
	_ = debugDir // Avoid unused variable warning

	return nil
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

	// Compute relative path from article to image
	// For index files, images are in _img/ subdirectory: _img/<slug>_header.png
	// For article files, images are alongside: <slug>_header.png
	articleDir := filepath.Dir(articlePath)
	imageDir := filepath.Dir(imagePath)
	imageFilename := filepath.Base(imagePath)

	var imageRef string
	if articleDir == imageDir {
		// Image is in same directory as article
		imageRef = imageFilename
	} else {
		// Image is in a subdirectory (e.g., _img/)
		relPath, err := filepath.Rel(articleDir, imagePath)
		if err != nil {
			imageRef = imageFilename
		} else {
			// Convert Windows backslashes to forward slashes for markdown
			imageRef = strings.ReplaceAll(relPath, "\\", "/")
		}
	}

	// Check if image is already present (check both with and without _img/ prefix)
	if strings.Contains(content, fmt.Sprintf("![Header](%s)", imageRef)) ||
		strings.Contains(content, fmt.Sprintf("![Header](%s)", imageFilename)) {
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
			newContentBuilder.WriteString(fmt.Sprintf("\n![Header](%s)\n", imageRef))
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

// isDockerAvailable checks if Docker daemon is running and accessible
func (a *Agent) isDockerAvailable() bool {
	cmd := exec.Command("docker", "info")
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

// ensureDockerRunning checks if Docker is available and attempts to start Docker Desktop on Windows if not
func (a *Agent) ensureDockerRunning() error {
	if a.isDockerAvailable() {
		return nil
	}

	log.Println("[Docker] Docker daemon not available, attempting to start...")

	if runtime.GOOS == "windows" {
		// Try to start Docker Desktop on Windows
		dockerDesktopPath := os.Getenv("DOCKER_DESKTOP_PATH")
		if dockerDesktopPath == "" {
			dockerDesktopPath = `C:\Program Files\Docker\Docker\Docker Desktop.exe`
		}

		// Check if Docker Desktop executable exists
		if _, err := os.Stat(dockerDesktopPath); os.IsNotExist(err) {
			return fmt.Errorf("Docker Desktop not found at %s. Please install Docker Desktop or set DOCKER_DESKTOP_PATH", dockerDesktopPath)
		}

		log.Printf("[Docker] Starting Docker Desktop: %s", dockerDesktopPath)
		cmd := exec.Command(dockerDesktopPath)
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("failed to start Docker Desktop: %w", err)
		}

		// Wait for Docker to be ready (up to 90 seconds)
		log.Println("[Docker] Waiting for Docker Desktop to be ready...")
		for i := 0; i < 45; i++ {
			if a.isDockerAvailable() {
				log.Println("[Docker] Docker Desktop is ready")
				return nil
			}
			time.Sleep(2 * time.Second)
		}

		return fmt.Errorf("Docker Desktop did not become ready within 90 seconds")
	}

	// On Linux/macOS, Docker should be started via systemd or similar
	return fmt.Errorf("Docker daemon not available. Please start Docker manually")
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

	// Check if ComfyUI is already running and healthy
	if client.IsHealthy(ctx) {
		log.Println("[ComfyUI] Already running and healthy")
		return client, nil
	}

	// Ensure Docker is running before attempting to start ComfyUI
	if err := a.ensureDockerRunning(); err != nil {
		return nil, fmt.Errorf("Docker not available: %w", err)
	}

	// Retry logic - try up to 3 times to get ComfyUI running
	maxRetries := 3
	for attempt := 1; attempt <= maxRetries; attempt++ {
		if attempt > 1 {
			log.Printf("[ComfyUI] Retry attempt %d/%d...", attempt, maxRetries)
			// Restart the container on retry
			log.Println("[ComfyUI] Restarting container...")
			_ = a.runDockerComposeCmd("docker compose restart comfyui")
			time.Sleep(5 * time.Second)
		} else {
			// First attempt - start the container
			startCmd := os.Getenv("COMFYUI_START_CMD")
			if startCmd == "" {
				startCmd = "docker compose up -d comfyui"
			}

			log.Printf("[ComfyUI] Starting with command: %s", startCmd)
			if err := a.runDockerComposeCmd(startCmd); err != nil {
				slog.Warn("Failed to start ComfyUI", "error", err)
				continue
			}
		}

		// Wait for ComfyUI to be ready (up to 2 minutes per attempt)
		log.Println("[ComfyUI] Waiting for ComfyUI to be ready...")
		waitTime := 60 // 60 iterations * 2 seconds = 2 minutes per attempt
		for i := 0; i < waitTime; i++ {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
			}

			if client.IsHealthy(ctx) {
				log.Println("[ComfyUI] Ready")
				return client, nil
			}

			// Check container status every 10 iterations to see if it's still running
			if i > 0 && i%10 == 0 {
				log.Printf("[ComfyUI] Still waiting... (%d/%d seconds)", i*2, waitTime*2)
				// Check if container is running
				if !a.isContainerRunning("comfyui") {
					log.Println("[ComfyUI] Container stopped unexpectedly, will retry")
					break
				}
			}

			time.Sleep(2 * time.Second)
		}

		if client.IsHealthy(ctx) {
			log.Println("[ComfyUI] Ready")
			return client, nil
		}

		log.Printf("[ComfyUI] Not ready after attempt %d", attempt)
	}

	return nil, fmt.Errorf("ComfyUI did not become ready after %d attempts", maxRetries)
}

// isContainerRunning checks if a Docker container is running
func (a *Agent) isContainerRunning(containerName string) bool {
	cmd := exec.Command("docker", "ps", "--filter", fmt.Sprintf("name=%s", containerName), "--filter", "status=running", "-q")
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	return len(strings.TrimSpace(string(output))) > 0
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

