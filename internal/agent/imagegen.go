package agent

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gitopedia/researcher/internal/comfyui"
	"github.com/gitopedia/researcher/internal/llm"
	"github.com/gitopedia/researcher/internal/styles"
	"golang.org/x/image/draw"
)

// PendingImagePrompt represents an image prompt waiting to be generated
type PendingImagePrompt struct {
	Topic          string
	PromptPath     string
	PromptText     string
	OutputPath     string
	ArticlePath    string
	ImageType      string // "header" or "section"
	Domain         string // Domain for style resolution (e.g., "technology")
	Category       string // Category for style resolution (e.g., "transportation")
	ArticleContent string // Full article content for generating unique prompts
}

// GeneratedCandidate represents a generated prompt ready for image generation
type GeneratedCandidate struct {
	Topic         string
	CandidateIdx  int
	PromptText    string
	OutputPath    string
	ImageType     string
	NegativeText  string
}

// GenerateImages runs the image generation process for all pending prompts.
// By default, organized-article images are written directly to their final _img location.
func (a *Agent) GenerateImages(ctx context.Context, branchName string) error {
	return a.generateImages(ctx, branchName, false)
}

// GenerateImagesForReview runs image generation with all article headers staged in
// Compendium/_incoming so they appear in the review UI before finalization.
func (a *Agent) GenerateImagesForReview(ctx context.Context, branchName string) error {
	return a.generateImages(ctx, branchName, true)
}

// generateImages runs the image generation process for all pending prompts.
// This should be called after the main research run, with Ollama stopped.
func (a *Agent) generateImages(ctx context.Context, branchName string, stageOrganizedInIncoming bool) error {
	log.Println("[Image Generation] Starting image generation process...")

	// Find all pending article image prompts
	pending, err := a.findPendingImagePrompts(branchName, stageOrganizedInIncoming)
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
					// Determine domain/category for style resolution based on index type
					indexDomain, indexCategory := "", ""
					switch p.IndexType {
					case "domain":
						indexDomain = p.Name // Domain indexes use their own name as domain
					case "category":
						indexDomain = p.Domain
						indexCategory = p.Name // Category indexes use their own name
					case "topic":
						indexDomain = p.Domain
						indexCategory = p.Category
					}

					// Get content for index image generation
					// Try reading existing index file first
					indexContent, _, _ := a.gh.GetFile(branchName, p.IndexPath)
					
					// If no index content, aggregate from child articles in _incoming
					if indexContent == "" {
						indexContent = a.aggregateArticleContent(branchName, p.IndexType, indexDomain, indexCategory, p.ChildItems)
					}

					// Add to pending list as a regular image prompt
					pending = append(pending, PendingImagePrompt{
						Topic:          p.Name + " (" + p.IndexType + " index)",
						PromptPath:     p.PromptPath,
						PromptText:     promptText,
						OutputPath:     p.OutputPath,
						ArticlePath:    p.IndexPath,
						ImageType:      "index_header",
						Domain:         indexDomain,
						Category:       indexCategory,
						ArticleContent: indexContent,
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

	// Load style manager for global config (negative/positive prompts, candidate count)
	configDir := getEnvOrDefault("CONFIG_DIR", "config")
	styleMgr := styles.NewManager(configDir)
	if err := styleMgr.Load(); err != nil {
		slog.Warn("Failed to load style config, using defaults", "error", err)
	}

	// Get global config values
	negativePrompts := styleMgr.GetNegativePrompts()
	negativePromptText := strings.Join(negativePrompts, ". ")
	candidateCount := styleMgr.GetCandidateCount()
	if candidateCount < 1 {
		candidateCount = 1
	}

	log.Printf("[Image Generation] Generating %d unique prompts per image", candidateCount)

	// ============================================================
	// PHASE 1: Generate unique prompts for each candidate (LLM calls)
	// This runs while Ollama is still available
	// ============================================================
	var allCandidates []GeneratedCandidate

	for _, p := range pending {
		if err := a.waitIfPaused(ctx); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			log.Println("[Image Generation] Cancelled during prompt generation")
			return ctx.Err()
		default:
		}

		log.Printf("[Prompt Generation] Generating %d unique prompts for '%s'...", candidateCount, p.Topic)

		// Extract visual elements once per article (expensive operation)
		var visualElements *llm.VisualElements
		var summary string

		if p.ArticleContent != "" {
			// Extract visual elements from the article
			visualReq := llm.VisualElementsRequest{
				Topic:          p.Topic,
				Domain:         p.Domain,
				Category:       p.Category,
				ArticleContent: p.ArticleContent,
			}
			var extractErr error
			visualElements, extractErr = a.llm.ExtractVisualElements(ctx, visualReq)
			if extractErr != nil {
				slog.Warn("Failed to extract visual elements, using defaults", "topic", p.Topic, "error", extractErr)
				visualElements = &llm.VisualElements{}
			}
			summary = extractArticleSummary(p.ArticleContent, 500)
		} else {
			// Fallback: use empty visual elements
			visualElements = &llm.VisualElements{}
			summary = ""
		}

		// Resolve styles and color moods for this domain/category
		resolved := styleMgr.ResolveAll(p.ImageType, strings.ToLower(p.Domain), strings.ToLower(p.Category))
		availableStyles := resolved.ArtisticStyles
		availableColorMoods := resolved.ColorMoods

		// Use defaults if none configured
		if len(availableStyles) == 0 {
			availableStyles = []string{"minimalist flat vector", "modern geometric abstract", "bauhaus design", "clean line art", "contemporary digital art"}
		}
		if len(availableColorMoods) == 0 {
			availableColorMoods = []string{"balanced and harmonious", "monochrome with subtle accents", "cool blue tones", "warm earth tones", "vibrant and energetic"}
		}

		// Generate a unique prompt for each candidate
		for candidateIdx := 1; candidateIdx <= candidateCount; candidateIdx++ {
			select {
			case <-ctx.Done():
				log.Println("[Image Generation] Cancelled during prompt generation")
				return ctx.Err()
			default:
			}

			// Calculate output path for this candidate
			candidatePath := strings.TrimSuffix(p.OutputPath, ".png") + fmt.Sprintf("_%d.png", candidateIdx)
			candidatePromptPath := strings.TrimSuffix(p.PromptPath, ".txt") + fmt.Sprintf("_%d.txt", candidateIdx)

			// Check if this candidate image already exists - skip if so
			if a.fileExists(branchName, candidatePath) {
				log.Printf("[Prompt Generation] Skipping candidate %d for '%s' - image already exists", candidateIdx, p.Topic)
				continue
			}

			// Check if prompt already exists - if so, reuse it instead of regenerating
			existingPrompt, _, promptErr := a.gh.GetFile(branchName, candidatePromptPath)
			if promptErr == nil && existingPrompt != "" {
				// Extract the prompt text from the existing file
				promptText := extractPromptText(existingPrompt)
				if promptText != "" {
					log.Printf("[Prompt Generation] Reusing existing prompt %d for '%s'", candidateIdx, p.Topic)
					allCandidates = append(allCandidates, GeneratedCandidate{
						Topic:        p.Topic,
						CandidateIdx: candidateIdx,
						PromptText:   promptText,
						OutputPath:   candidatePath,
						ImageType:    p.ImageType,
						NegativeText: negativePromptText,
					})
					continue
				}
			}

			// Randomly select styles and color mood for this candidate
			numStyles := 1
			if len(availableStyles) > 2 {
				numStyles = 2
			}
			selectedStyles := styleMgr.SelectRandomStyles(availableStyles, numStyles)
			colorMood := styleMgr.SelectRandomColorMood(availableColorMoods)

			// Get random background guidance (dark or light)
			backgroundGuidance := styleMgr.GetBackgroundGuidance()

			// Build the LLM request for unique prompt generation
			req := llm.ImagePromptRequest{
				Topic:              p.Topic,
				Domain:             p.Domain,
				Category:           p.Category,
				ArticleSummary:     summary,
				ExtractedElements:  visualElements,
				ColorMood:          colorMood,
				ArtisticStyles:     selectedStyles,
				CategoryGuidance:   resolved.Guidance,
				BackgroundGuidance: backgroundGuidance,
			}

			// Generate unique prompt via LLM
			result, err := a.llm.GenerateImagePrompt(ctx, req)
			if err != nil {
				slog.Error("Failed to generate prompt", "topic", p.Topic, "candidate", candidateIdx, "error", err)
				continue
			}

			allCandidates = append(allCandidates, GeneratedCandidate{
				Topic:        p.Topic,
				CandidateIdx: candidateIdx,
				PromptText:   result.Prompt,
				OutputPath:   candidatePath,
				ImageType:    p.ImageType,
				NegativeText: negativePromptText,
			})

			log.Printf("[Prompt Generation] Generated prompt %d/%d for '%s' (styles: %v, mood: %s)",
				candidateIdx, candidateCount, p.Topic, selectedStyles, colorMood)

			// Save the unique prompt to debug folder
			candidatePromptContent := fmt.Sprintf("# Unique Prompt %d for: %s\n", candidateIdx, p.Topic)
			candidatePromptContent += fmt.Sprintf("# Styles: %v\n", selectedStyles)
			candidatePromptContent += fmt.Sprintf("# Color Mood: %s\n", colorMood)
			candidatePromptContent += fmt.Sprintf("# Background: %s\n", strings.TrimSpace(backgroundGuidance))
			candidatePromptContent += fmt.Sprintf("# Negative Prompts: %s\n\n", negativePromptText)
			candidatePromptContent += result.Prompt
			
			if err := a.gh.CreateFile(branchName, candidatePromptPath, fmt.Sprintf("Add prompt %d for %s", candidateIdx, p.Topic), candidatePromptContent); err != nil {
				slog.Warn("Failed to save candidate prompt", "path", candidatePromptPath, "error", err)
			}
		}
	}

	if len(allCandidates) == 0 {
		log.Println("[Image Generation] No prompts were generated")
		return nil
	}

	log.Printf("[Image Generation] Generated %d total unique prompts, now generating images...", len(allCandidates))

	// ============================================================
	// PHASE 2: Generate images from the unique prompts
	// ComfyUI is expected to already be running externally
	// ============================================================

	comfyURL := os.Getenv("COMFYUI_URL")
	if comfyURL == "" {
		comfyURL = "http://localhost:8188"
	}
	comfyClient := comfyui.NewClient(comfyURL)
	if !comfyClient.IsHealthy(ctx) {
		return fmt.Errorf("ComfyUI is not available at %s – please start it externally", comfyURL)
	}

	if len(negativePrompts) > 0 {
		log.Printf("[Image Generation] Using negative prompt: %s", negativePromptText)
	}

	// Generate images from the stored prompts
	generatedCount := 0
	errorCount := 0

	for _, candidate := range allCandidates {
		if err := a.waitIfPaused(ctx); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			log.Println("[Image Generation] Cancelled")
			return ctx.Err()
		default:
		}

		// Set image dimensions based on type
		opts := comfyui.DefaultOptions()
		if candidate.ImageType == "header" || candidate.ImageType == "index_header" {
			opts.Width = 1920
			opts.Height = 1080
		} else {
			// Section images are smaller (1024x768 landscape)
			opts.Width = 1024
			opts.Height = 768
		}

		// Set negative prompt
		opts.NegativePrompt = candidate.NegativeText

		// Generate with a fresh random seed
		opts.Seed = -1

		log.Printf("[Image Generation] Generating image %d/%d: '%s' candidate %d",
			generatedCount+errorCount+1, len(allCandidates), candidate.Topic, candidate.CandidateIdx)

		imageData, err := comfyClient.GenerateImage(ctx, candidate.PromptText, &opts)
		if err != nil {
			slog.Error("Failed to generate image", "topic", candidate.Topic, "candidate", candidate.CandidateIdx, "error", err)
			errorCount++
			continue
		}

		// Save the image
		if err := a.saveCandidateImage(branchName, candidate.Topic, candidate.OutputPath, imageData); err != nil {
			slog.Error("Failed to save image", "topic", candidate.Topic, "candidate", candidate.CandidateIdx, "error", err)
			errorCount++
			continue
		}

		generatedCount++
		log.Printf("[Image Generation] Generated '%s' candidate %d -> %s", candidate.Topic, candidate.CandidateIdx, candidate.OutputPath)
	}

	log.Printf("[Image Generation] Completed: %d generated, %d errors", generatedCount, errorCount)

	return nil
}

// findPendingImagePrompts finds all image prompts that haven't been generated yet.
// When stageOrganizedInIncoming is true, organized article header images are staged
// under Compendium/_incoming for UI review.
func (a *Agent) findPendingImagePrompts(branchName string, stageOrganizedInIncoming bool) ([]PendingImagePrompt, error) {
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
				// Try to find the article - first in _incoming, then in organized location.
				articlePath := filepath.Join("Compendium/_incoming", articleDir+".md")
				outputPath := filepath.Join("Compendium/_incoming", articleDir+"_header.png")
				isOrganized := false

				// Read domain/category and full content from article frontmatter
				domain, category, topic := "", "", ""
				articleContent, _, articleErr := a.gh.GetFile(branchName, articlePath)
				
				if articleErr != nil {
					// Article not in _incoming, try to find it in organized location
					organizedPath, orgDomain, orgCategory, orgTopic := a.findOrganizedArticle(branchName, articleDir)
					if organizedPath != "" {
						articlePath = organizedPath
						articleContent, _, articleErr = a.gh.GetFile(branchName, articlePath)
						if articleErr == nil {
							domain = orgDomain
							category = orgCategory
							topic = orgTopic
							isOrganized = true
							// By default organized articles output to their topic _img folder.
							// For backfill-review mode, keep output in _incoming so the dashboard
							// images page can review candidates consistently.
							if !stageOrganizedInIncoming {
								topicPath := filepath.Dir(articlePath)
								outputPath = filepath.Join(topicPath, "_img", articleDir+"_header.png")
							}
						}
					}
				}

				if articleErr == nil {
					if domain == "" {
						domain = extractFrontmatterField(articleContent, "domain")
					}
					if category == "" {
						category = extractFrontmatterField(articleContent, "category")
					}
					if topic == "" {
						topic = extractFrontmatterField(articleContent, "topic")
					}
				}

				// Check if image already exists (check both .png and .avif)
				imageExists := false
				if isOrganized && !stageOrganizedInIncoming {
					topicPath := filepath.Dir(articlePath)
					imagePathBase := filepath.Join(topicPath, "_img", articleDir+"_header")
					imageExists = a.imageExistsAnyFormat(branchName, imagePathBase)
				} else if isOrganized && stageOrganizedInIncoming {
					// In review-staging mode, treat either final _img or staged _incoming
					// canonical image as already present.
					topicPath := filepath.Dir(articlePath)
					finalPathBase := filepath.Join(topicPath, "_img", articleDir+"_header")
					stagedPathBase := filepath.Join("Compendium/_incoming", articleDir+"_header")
					imageExists = a.imageExistsAnyFormat(branchName, finalPathBase) ||
						a.imageExistsAnyFormat(branchName, stagedPathBase)
				} else {
					_, _, err = a.gh.GetFile(branchName, outputPath)
					imageExists = err == nil
				}

				if !imageExists {
					// Image doesn't exist, add to pending
					pending = append(pending, PendingImagePrompt{
						Topic:          articleDir,
						PromptPath:     headerPromptPath,
						PromptText:     promptText,
						OutputPath:     outputPath,
						ArticlePath:    articlePath,
						ImageType:      "header",
						Domain:         domain,
						Category:       category,
						ArticleContent: articleContent,
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

			// Read domain/category and full content from article frontmatter for sections too
			sectionDomain, sectionCategory := "", ""
			sectionArticleContent, _, sectionArticleErr := a.gh.GetFile(branchName, articlePath)
			if sectionArticleErr == nil {
				sectionDomain = extractFrontmatterField(sectionArticleContent, "domain")
				sectionCategory = extractFrontmatterField(sectionArticleContent, "category")
			}

			_, _, err = a.gh.GetFile(branchName, outputPath)
			if err != nil {
				// Image doesn't exist, add to pending
				pending = append(pending, PendingImagePrompt{
					Topic:          articleDir + " - " + sectionSlug,
					PromptPath:     sectionPromptPath,
					PromptText:     promptText,
					OutputPath:     outputPath,
					ArticlePath:    articlePath,
					ImageType:      "section",
					Domain:         sectionDomain,
					Category:       sectionCategory,
					ArticleContent: sectionArticleContent,
				})
			}
		}
	}

	return pending, nil
}

// PendingIndexImagePrompt represents an index image prompt waiting to be generated
type PendingIndexImagePrompt struct {
	IndexType    string // "domain", "category", or "topic"
	Name         string // Display name (e.g., "Science", "Physics", "Quantum Mechanics")
	Slug         string // URL slug (e.g., "science", "physics", "quantum-mechanics")
	Domain       string // Parent domain (empty for domain-level)
	DomainSlug   string
	Category     string // Parent category (empty for domain/category-level)
	CategorySlug string
	ChildItems   []string // Categories/topics/articles contained
	PromptPath   string   // Path to save the prompt
	OutputPath   string   // Path for the generated image (in _incoming)
	FinalPath    string   // Final destination path after organize
	IndexPath    string   // Path to the index.md file
}

// findPendingIndexImagePrompts scans the Compendium directory AND _incoming articles for index files missing header images
func (a *Agent) findPendingIndexImagePrompts(branchName string) ([]PendingIndexImagePrompt, error) {
	var pending []PendingIndexImagePrompt

	// First, scan _incoming articles to find domains/categories/topics that need index images
	incomingPending, err := a.findIndexImagesFromIncoming(branchName)
	if err != nil {
		slog.Warn("Failed to scan incoming articles for index images", "error", err)
	} else {
		pending = append(pending, incomingPending...)
	}

	// Then scan existing Compendium structure for any missing index images
	// List top-level directories in Compendium (domains)
	compendiumPath := "Compendium"
	domains, err := a.gh.ListDirectory(branchName, compendiumPath)
	if err != nil {
		return pending, nil // Return what we have from incoming
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

		// Check if domain header image exists (check both staging and final locations, and both .png and .avif)
		domainFinalPathBase := filepath.Join(domainPath, "_img", domainDir+"_header")
		domainStagingPath := filepath.Join("Compendium/_incoming/indexes/domains", domainDir+"_header.png")
		domainImageExists := a.imageExistsAnyFormat(branchName, domainFinalPathBase) ||
			a.fileExists(branchName, domainStagingPath)
		if !domainImageExists {
			// Image doesn't exist in either location - get child categories
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
				OutputPath: domainStagingPath,
				FinalPath:  domainFinalPathBase + ".png",
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

			// Check if category header image exists (check both staging and final locations, and both .png and .avif)
			categoryFinalPathBase := filepath.Join(categoryPath, "_img", categoryDir+"_header")
			categoryStagingPath := filepath.Join("Compendium/_incoming/indexes/categories", domainDir, categoryDir+"_header.png")
			legacyCategoryStagingPath := filepath.Join("Compendium/_incoming/indexes/categories", domainDir+"--"+categoryDir+"_header.png")
			categoryImageExists := a.imageExistsAnyFormat(branchName, categoryFinalPathBase) ||
				a.fileExists(branchName, categoryStagingPath) ||
				a.fileExists(branchName, legacyCategoryStagingPath)
			if !categoryImageExists {
				// Image doesn't exist in either location - get child topics
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
					OutputPath:   categoryStagingPath,
					FinalPath:    categoryFinalPathBase + ".png",
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

				// Check if topic header image exists (check both staging and final locations, and both .png and .avif)
				topicFinalPathBase := filepath.Join(topicPath, "_img", topicDir+"_header")
				topicStagingPath := filepath.Join("Compendium/_incoming/indexes/topics", domainDir, categoryDir, topicDir+"_header.png")
				legacyTopicStagingPath := filepath.Join("Compendium/_incoming/indexes/topics", domainDir+"--"+categoryDir+"--"+topicDir+"_header.png")
				topicImageExists := a.imageExistsAnyFormat(branchName, topicFinalPathBase) ||
					a.fileExists(branchName, topicStagingPath) ||
					a.fileExists(branchName, legacyTopicStagingPath)
				if !topicImageExists {
					// Image doesn't exist in either location - get child articles
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
						OutputPath:   topicStagingPath,
						FinalPath:    topicFinalPathBase + ".png",
						IndexPath:    topicIndexPath,
					})
				}
			}
		}
	}

	return pending, nil
}

// IndexInfo holds information about a domain/category/topic extracted from article frontmatter
type IndexInfo struct {
	Domain       string
	DomainSlug   string
	Category     string
	CategorySlug string
	Topic        string
	TopicSlug    string
	Articles     []string // Article slugs belonging to this index
}

// findIndexImagesFromIncoming scans articles in _incoming to find domains/categories/topics needing index images
func (a *Agent) findIndexImagesFromIncoming(branchName string) ([]PendingIndexImagePrompt, error) {
	var pending []PendingIndexImagePrompt

	// Track unique domains, categories, and topics from incoming articles
	domains := make(map[string]*IndexInfo)    // key: domainSlug
	categories := make(map[string]*IndexInfo) // key: domainSlug--categorySlug
	topics := make(map[string]*IndexInfo)     // key: domainSlug--categorySlug--topicSlug

	// List incoming articles
	incomingPath := "Compendium/_incoming"
	files, err := a.gh.ListDirectory(branchName, incomingPath)
	if err != nil {
		return nil, fmt.Errorf("failed to list incoming directory: %w", err)
	}

	for _, file := range files {
		if !strings.HasSuffix(file, ".md") {
			continue
		}

		articlePath := filepath.Join(incomingPath, file)
		content, _, err := a.gh.GetFile(branchName, articlePath)
		if err != nil {
			continue
		}

		// Extract index info from frontmatter
		domain := extractFrontmatterValue(content, "domain")
		domainSlug := extractFrontmatterValue(content, "domain-slug")
		category := extractFrontmatterValue(content, "category")
		categorySlug := extractFrontmatterValue(content, "category-slug")
		topic := extractFrontmatterValue(content, "topic")
		topicSlug := extractFrontmatterValue(content, "topic-slug")
		articleSlug := strings.TrimSuffix(file, ".md")

		if domainSlug == "" {
			continue // Skip articles without proper frontmatter
		}

		// Track domain
		if _, exists := domains[domainSlug]; !exists {
			domains[domainSlug] = &IndexInfo{
				Domain:     domain,
				DomainSlug: domainSlug,
			}
		}

		// Track category
		if categorySlug != "" {
			catKey := domainSlug + "--" + categorySlug
			if _, exists := categories[catKey]; !exists {
				categories[catKey] = &IndexInfo{
					Domain:       domain,
					DomainSlug:   domainSlug,
					Category:     category,
					CategorySlug: categorySlug,
				}
			}
		}

		// Track topic and its articles
		if topicSlug != "" {
			topicKey := domainSlug + "--" + categorySlug + "--" + topicSlug
			if _, exists := topics[topicKey]; !exists {
				topics[topicKey] = &IndexInfo{
					Domain:       domain,
					DomainSlug:   domainSlug,
					Category:     category,
					CategorySlug: categorySlug,
					Topic:        topic,
					TopicSlug:    topicSlug,
					Articles:     []string{},
				}
			}
			topics[topicKey].Articles = append(topics[topicKey].Articles, articleSlug)
		}
	}

	// Now check which index images are missing and add to pending

	// Check domains
	for _, info := range domains {
		domainFinalPathBase := filepath.Join("Compendium", info.DomainSlug, "_img", info.DomainSlug+"_header")
		domainStagingPath := filepath.Join("Compendium/_incoming/indexes/domains", info.DomainSlug+"_header.png")

		domainImageExists := a.imageExistsAnyFormat(branchName, domainFinalPathBase) ||
			a.fileExists(branchName, domainStagingPath)

		if !domainImageExists {
			// Collect child categories for this domain
			var childItems []string
			for _, cat := range categories {
				if cat.DomainSlug == info.DomainSlug {
					childItems = append(childItems, cat.Category)
				}
			}

			pending = append(pending, PendingIndexImagePrompt{
				IndexType:  "domain",
				Name:       info.Domain,
				Slug:       info.DomainSlug,
				ChildItems: childItems,
				PromptPath: filepath.Join("Compendium/_debug/indexes", info.DomainSlug, "header_image_prompt.txt"),
				OutputPath: domainStagingPath,
				FinalPath:  domainFinalPathBase + ".png",
			})
			log.Printf("[Index Discovery] Found new domain needing image: %s", info.Domain)
		}
	}

	// Check categories
	for _, info := range categories {
		categoryFinalPathBase := filepath.Join("Compendium", info.DomainSlug, info.CategorySlug, "_img", info.CategorySlug+"_header")
		categoryStagingPath := filepath.Join("Compendium/_incoming/indexes/categories", info.DomainSlug, info.CategorySlug+"_header.png")
		legacyCategoryStagingPath := filepath.Join("Compendium/_incoming/indexes/categories", info.DomainSlug+"--"+info.CategorySlug+"_header.png")

		categoryImageExists := a.imageExistsAnyFormat(branchName, categoryFinalPathBase) ||
			a.fileExists(branchName, categoryStagingPath) ||
			a.fileExists(branchName, legacyCategoryStagingPath)

		if !categoryImageExists {
			// Collect child topics for this category
			var childItems []string
			for _, top := range topics {
				if top.DomainSlug == info.DomainSlug && top.CategorySlug == info.CategorySlug {
					childItems = append(childItems, top.Topic)
				}
			}

			pending = append(pending, PendingIndexImagePrompt{
				IndexType:    "category",
				Name:         info.Category,
				Slug:         info.CategorySlug,
				Domain:       info.Domain,
				DomainSlug:   info.DomainSlug,
				ChildItems:   childItems,
				PromptPath:   filepath.Join("Compendium/_debug/indexes", info.DomainSlug, info.CategorySlug, "header_image_prompt.txt"),
				OutputPath:   categoryStagingPath,
				FinalPath:    categoryFinalPathBase + ".png",
			})
			log.Printf("[Index Discovery] Found new category needing image: %s > %s", info.Domain, info.Category)
		}
	}

	// Check topics
	for _, info := range topics {
		topicFinalPathBase := filepath.Join("Compendium", info.DomainSlug, info.CategorySlug, info.TopicSlug, "_img", info.TopicSlug+"_header")
		topicStagingPath := filepath.Join("Compendium/_incoming/indexes/topics", info.DomainSlug, info.CategorySlug, info.TopicSlug+"_header.png")
		legacyTopicStagingPath := filepath.Join("Compendium/_incoming/indexes/topics", info.DomainSlug+"--"+info.CategorySlug+"--"+info.TopicSlug+"_header.png")

		topicImageExists := a.imageExistsAnyFormat(branchName, topicFinalPathBase) ||
			a.fileExists(branchName, topicStagingPath) ||
			a.fileExists(branchName, legacyTopicStagingPath)

		if !topicImageExists {
			pending = append(pending, PendingIndexImagePrompt{
				IndexType:    "topic",
				Name:         info.Topic,
				Slug:         info.TopicSlug,
				Domain:       info.Domain,
				DomainSlug:   info.DomainSlug,
				Category:     info.Category,
				CategorySlug: info.CategorySlug,
				ChildItems:   info.Articles,
				PromptPath:   filepath.Join("Compendium/_debug/indexes", info.DomainSlug, info.CategorySlug, info.TopicSlug, "header_image_prompt.txt"),
				OutputPath:   topicStagingPath,
				FinalPath:    topicFinalPathBase + ".png",
			})
			log.Printf("[Index Discovery] Found new topic needing image: %s > %s > %s", info.Domain, info.Category, info.Topic)
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

// aggregateArticleContent collects content from articles that match the given index criteria.
// It scans both _incoming and the relevant Compendium directory structure.
// This ensures index images are based on actual article content, not hallucinated concepts.
func (a *Agent) aggregateArticleContent(branchName, indexType, domain, category string, childItems []string) string {
	var contentParts []string
	maxArticles := 20 // Limit to keep content manageable
	articlesFound := 0
	seenTitles := make(map[string]bool) // Track titles to avoid duplicates

	// Helper to add article content
	addArticle := func(content string) bool {
		if articlesFound >= maxArticles {
			return false
		}
		summary := extractArticleSummary(content, 500)
		if summary == "" {
			return true // Continue but don't count
		}
		title := extractFrontmatterValue(content, "title")
		if title != "" {
			if seenTitles[title] {
				return true // Skip duplicate
			}
			seenTitles[title] = true
			contentParts = append(contentParts, fmt.Sprintf("Article: %s\n%s", title, summary))
		} else {
			contentParts = append(contentParts, summary)
		}
		articlesFound++
		return articlesFound < maxArticles
	}

	// PHASE 1: Scan _incoming for matching articles
	incomingPath := "Compendium/_incoming"
	if files, err := a.gh.ListDirectory(branchName, incomingPath); err == nil {
		for _, file := range files {
			if !strings.HasSuffix(file, ".md") {
				continue
			}

			articlePath := filepath.Join(incomingPath, file)
			content, _, err := a.gh.GetFile(branchName, articlePath)
			if err != nil {
				continue
			}

			// Extract frontmatter values
			articleDomain := extractFrontmatterValue(content, "domain")
			articleDomainSlug := extractFrontmatterValue(content, "domain-slug")
			articleCategory := extractFrontmatterValue(content, "category")
			articleCategorySlug := extractFrontmatterValue(content, "category-slug")

			// Match based on index type
			match := false
			switch indexType {
			case "domain":
				match = strings.EqualFold(articleDomain, domain) || strings.EqualFold(articleDomainSlug, strings.ToLower(domain))
			case "category":
				domainMatch := strings.EqualFold(articleDomain, domain) || strings.EqualFold(articleDomainSlug, strings.ToLower(domain))
				categoryMatch := strings.EqualFold(articleCategory, category) || strings.EqualFold(articleCategorySlug, strings.ToLower(category))
				match = domainMatch && categoryMatch
			case "topic":
				domainMatch := strings.EqualFold(articleDomain, domain) || strings.EqualFold(articleDomainSlug, strings.ToLower(domain))
				categoryMatch := strings.EqualFold(articleCategory, category) || strings.EqualFold(articleCategorySlug, strings.ToLower(category))
				match = domainMatch && categoryMatch
			}

			if match {
				if !addArticle(content) {
					break // Hit max articles
				}
			}
		}
	}

	// PHASE 2: Scan relevant Compendium directory structure
	if articlesFound < maxArticles {
		var compendiumPath string
		domainSlug := strings.ToLower(strings.ReplaceAll(domain, " ", "-"))
		categorySlug := strings.ToLower(strings.ReplaceAll(category, " ", "-"))

		switch indexType {
		case "domain":
			compendiumPath = filepath.Join("Compendium", domainSlug)
		case "category":
			compendiumPath = filepath.Join("Compendium", domainSlug, categorySlug)
		case "topic":
			// For topics, category is actually the topic slug in this context
			compendiumPath = filepath.Join("Compendium", domainSlug, categorySlug)
		}

		if compendiumPath != "" {
			a.scanCompendiumDirectory(branchName, compendiumPath, addArticle)
		}
	}

	if len(contentParts) == 0 {
		// Fallback: provide child items as context
		if len(childItems) > 0 {
			return fmt.Sprintf("This index covers the following topics: %s", strings.Join(childItems, ", "))
		}
		return ""
	}

	result := fmt.Sprintf("Content from articles in %s:\n\n%s", domain, strings.Join(contentParts, "\n\n---\n\n"))
	log.Printf("[Index Content] Aggregated %d articles for %s index (%d chars)", articlesFound, indexType, len(result))
	return result
}

// scanCompendiumDirectory recursively scans a directory for .md article files
func (a *Agent) scanCompendiumDirectory(branchName, dirPath string, addArticle func(string) bool) {
	entries, err := a.gh.ListDirectory(branchName, dirPath)
	if err != nil {
		return
	}

	for _, entry := range entries {
		// Skip special directories and files
		if strings.HasPrefix(entry, "_") || entry == "img" {
			continue
		}

		entryPath := filepath.Join(dirPath, entry)

		if strings.HasSuffix(entry, ".md") && entry != "index.md" {
			// It's an article file
			content, _, err := a.gh.GetFile(branchName, entryPath)
			if err != nil {
				continue
			}
			if !addArticle(content) {
				return // Hit max articles
			}
		} else if !strings.Contains(entry, ".") {
			// It's likely a subdirectory, recurse into it
			a.scanCompendiumDirectory(branchName, entryPath, addArticle)
		}
	}
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

// saveCandidateImage saves a candidate image to the repository (no medium variant)
func (a *Agent) saveCandidateImage(branchName, topic, outputPath string, imageData []byte) error {
	message := fmt.Sprintf("Add image candidate for %s", topic)
	return a.gh.AddBinaryFile(branchName, outputPath, message, imageData)
}

// saveGeneratedImage saves the generated image to the repository
func (a *Agent) saveGeneratedImage(branchName string, p PendingImagePrompt, imageData []byte) error {
	message := fmt.Sprintf("Add header image for %s", p.Topic)
	if err := a.gh.AddBinaryFile(branchName, p.OutputPath, message, imageData); err != nil {
		return err
	}

	// Skip medium variant generation - will be done during manual review phase
	return nil
}

// resizeImage resizes a PNG image to the specified width while maintaining aspect ratio
func resizeImage(imageData []byte, targetWidth int) ([]byte, error) {
	// Decode the original image
	img, _, err := image.Decode(bytes.NewReader(imageData))
	if err != nil {
		return nil, fmt.Errorf("failed to decode image: %w", err)
	}

	// Calculate new dimensions maintaining aspect ratio
	bounds := img.Bounds()
	origWidth := bounds.Dx()
	origHeight := bounds.Dy()

	if origWidth <= targetWidth {
		// Image is already smaller than target, return original
		return imageData, nil
	}

	newWidth := targetWidth
	newHeight := int(float64(origHeight) * float64(targetWidth) / float64(origWidth))

	// Create destination image
	dst := image.NewRGBA(image.Rect(0, 0, newWidth, newHeight))

	// Resize using high-quality CatmullRom interpolation
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Over, nil)

	// Encode to PNG
	var buf bytes.Buffer
	if err := png.Encode(&buf, dst); err != nil {
		return nil, fmt.Errorf("failed to encode resized image: %w", err)
	}

	return buf.Bytes(), nil
}

// insertHeaderImageInMarkdown inserts the header image reference into the article markdown
func (a *Agent) insertHeaderImageInMarkdown(branchName, articlePath, imagePath string) error {
	content, sha, err := a.gh.GetFile(branchName, articlePath)
	if err != nil {
		return fmt.Errorf("failed to read article file: %w", err)
	}

	// Compute relative path from article to image
	// For index files, images are in _img/ subdirectory: _img/<slug>_header.avif
	// For article files, images are in _img/ subdirectory: _img/<slug>_header.avif
	imageFilename := filepath.Base(imagePath)
	
	// Extract slug from image filename (e.g., "quantum-mechanics_header.png" -> "quantum-mechanics")
	slug := strings.TrimSuffix(imageFilename, filepath.Ext(imageFilename))
	slug = strings.TrimSuffix(slug, "_header")
	slug = strings.TrimSuffix(slug, "-medium")
	
	// Always use _img/ directory and .avif extension for the markdown reference
	imageRef := fmt.Sprintf("_img/%s_header.avif", slug)

	// Check if any header image reference already exists (any extension, any path format)
	// This prevents duplicate insertions
	headerPattern := regexp.MustCompile(`!\[Header\]\([^)]+\)`)
	if headerPattern.MatchString(content) {
		log.Printf("[Header Image] Header reference already present in %s, skipping insertion", articlePath)
		return nil
	}

	// Also check for header image in _img directory (with any extension)
	imgDirPattern := regexp.MustCompile(`!\[Header\]\(_img/[^)]+\)`)
	if imgDirPattern.MatchString(content) {
		log.Printf("[Header Image] Header image in _img/ already present in %s, skipping insertion", articlePath)
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
			// Insert image reference after the frontmatter (using _img/ and .avif)
			newContentBuilder.WriteString(fmt.Sprintf("\n![Header](%s)\n", imageRef))
		}
	}

	newContent := strings.TrimSuffix(newContentBuilder.String(), "\n")
	
	// Log the image path for debugging
	log.Printf("[Header Image] Inserting reference %s into %s (source: %s)", imageRef, articlePath, imagePath)
	
	return a.gh.UpdateFile(branchName, articlePath, fmt.Sprintf("Add header image to %s", filepath.Base(articlePath)), newContent, sha)
}

// hasExistingHeaderImage checks if a markdown file already has a header image reference
func hasExistingHeaderImage(content string) bool {
	headerPattern := regexp.MustCompile(`!\[Header\]\([^)]+\)`)
	return headerPattern.MatchString(content)
}

// BackfillImagePrompts generates image prompts for existing articles that don't have them
func (a *Agent) BackfillImagePrompts(ctx context.Context, branchName string) error {
	log.Println("[Backfill] Starting image prompt backfill...")

	backfilledCount := 0

	// First, check incoming articles
	incomingPath := "Compendium/_incoming"
	files, err := a.gh.ListDirectory(branchName, incomingPath)
	if err == nil {
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
	}

	// Second, scan organized articles in Compendium/<domain>/<category>/<topic>/*.md
	log.Println("[Backfill] Scanning organized articles...")
	organizedCount, err := a.backfillOrganizedArticles(ctx, branchName)
	if err != nil {
		slog.Warn("Error scanning organized articles", "error", err)
	}
	backfilledCount += organizedCount

	log.Printf("[Backfill] Completed: %d prompts generated", backfilledCount)
	return nil
}

// backfillOrganizedArticles scans organized articles and generates prompts for those missing them
func (a *Agent) backfillOrganizedArticles(ctx context.Context, branchName string) (int, error) {
	backfilledCount := 0

	// List domains
	domains, err := a.gh.ListDirectory(branchName, "Compendium")
	if err != nil {
		return 0, fmt.Errorf("failed to list Compendium: %w", err)
	}

	for _, domainDir := range domains {
		// Skip special directories
		if strings.HasPrefix(domainDir, "_") || domainDir == "index.md" {
			continue
		}

		domainPath := filepath.Join("Compendium", domainDir)
		categories, err := a.gh.ListDirectory(branchName, domainPath)
		if err != nil {
			continue
		}

		for _, categoryDir := range categories {
			if strings.HasPrefix(categoryDir, "_") || categoryDir == "index.md" || categoryDir == "img" {
				continue
			}

			categoryPath := filepath.Join(domainPath, categoryDir)
			topics, err := a.gh.ListDirectory(branchName, categoryPath)
			if err != nil {
				continue
			}

			for _, topicDir := range topics {
				if strings.HasPrefix(topicDir, "_") || topicDir == "index.md" || topicDir == "img" {
					continue
				}

				topicPath := filepath.Join(categoryPath, topicDir)
				articles, err := a.gh.ListDirectory(branchName, topicPath)
				if err != nil {
					continue
				}

				for _, articleFile := range articles {
					if !strings.HasSuffix(articleFile, ".md") || articleFile == "index.md" {
						continue
					}

					select {
					case <-ctx.Done():
						return backfilledCount, ctx.Err()
					default:
					}

					articleSlug := strings.TrimSuffix(articleFile, ".md")

					// Check if prompt already exists
					promptPath := filepath.Join("Compendium/_debug/articles", articleSlug, "header_image_prompt.txt")
					if _, _, err := a.gh.GetFile(branchName, promptPath); err == nil {
						continue // Prompt already exists
					}

					// Check if image already exists (in _img directory)
					imagePathBase := filepath.Join(topicPath, "_img", articleSlug+"_header")
					if a.imageExistsAnyFormat(branchName, imagePathBase) {
						continue // Image already exists
					}

					// Get article content
					articlePath := filepath.Join(topicPath, articleFile)
					content, _, err := a.gh.GetFile(branchName, articlePath)
					if err != nil {
						slog.Warn("Failed to read organized article", "path", articlePath, "error", err)
						continue
					}

					// Extract domain and category from frontmatter
					domain := extractFrontmatterField(content, "domain")
					category := extractFrontmatterField(content, "category")
					if domain == "" {
						domain = domainDir
					}
					if category == "" {
						category = categoryDir
					}

					log.Printf("[Backfill] Generating prompt for organized article %s (domain: %s, category: %s)...", articleSlug, domain, category)

					// Generate prompt using the organized article path
					if err := a.generateHeaderImagePromptForOrganizedArticle(ctx, articleSlug, branchName, domain, category, articlePath); err != nil {
						slog.Error("Failed to generate prompt for organized article", "article", articleSlug, "error", err)
						continue
					}

					backfilledCount++
				}
			}
		}
	}

	return backfilledCount, nil
}

// generateHeaderImagePromptForOrganizedArticle generates an image prompt for an organized article
func (a *Agent) generateHeaderImagePromptForOrganizedArticle(ctx context.Context, articleSlug, branchName, domain, category, articlePath string) error {
	articleTitle := cleanTopic(articleSlug)

	log.Printf("[Image Prompt] Generating header image prompt for organized article '%s' (%s > %s)", articleTitle, domain, category)

	// Load the article content from its organized location
	articleContent, _, err := a.gh.GetFile(branchName, articlePath)
	if err != nil {
		return fmt.Errorf("failed to load article: %w", err)
	}

	// Step 1: Extract article-specific visual elements using LLM
	log.Printf("[Image Prompt] Extracting visual elements from article...")
	visualReq := llm.VisualElementsRequest{
		Topic:          articleTitle,
		Domain:         domain,
		Category:       category,
		ArticleContent: articleContent,
	}
	visualElements, err := a.llm.ExtractVisualElements(ctx, visualReq)
	if err != nil {
		slog.Warn("Failed to extract visual elements, using defaults", "error", err)
		visualElements = &llm.VisualElements{}
	}

	// Extract article summary for additional context
	summary := extractArticleSummary(articleContent, 500)

	// Get config directory path
	configDir := getEnvOrDefault("CONFIG_DIR", "config")

	// Load style configuration
	styleMgr := styles.NewManager(configDir)
	if err := styleMgr.Load(); err != nil {
		return fmt.Errorf("failed to load style config: %w", err)
	}

	// Resolve category-based configuration (for styles and colors)
	resolved := styleMgr.ResolveAll("header", strings.ToLower(domain), strings.ToLower(category))

	// Select 1-2 random artistic styles from config
	numStyles := 1
	if len(resolved.ArtisticStyles) > 2 {
		numStyles = 2
	}
	selectedStyles := styleMgr.SelectRandomStyles(resolved.ArtisticStyles, numStyles)

	// Select a random color mood from config
	colorMood := styleMgr.SelectRandomColorMood(resolved.ColorMoods)

	// Step 2: Generate the image prompt with structured elements
	req := llm.ImagePromptRequest{
		Topic:             articleTitle,
		Domain:            domain,
		Category:          category,
		ArticleSummary:    summary,
		ExtractedElements: visualElements,
		ColorMood:         colorMood,
		ArtisticStyles:    selectedStyles,
		CategoryGuidance:  resolved.Guidance,
	}

	result, err := a.llm.GenerateImagePrompt(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to generate image prompt: %w", err)
	}

	// Save the prompt to the debug folder
	debugDir := debugBasePath(articleSlug)
	promptPath := fmt.Sprintf("%s/header_image_prompt.txt", debugDir)
	promptContent := fmt.Sprintf("# Header Image Prompt for: %s\n", articleTitle)
	promptContent += fmt.Sprintf("# Domain > Category: %s > %s\n", domain, category)
	promptContent += fmt.Sprintf("# Styles: %s\n", strings.Join(selectedStyles, ", "))
	promptContent += fmt.Sprintf("# Color Mood: %s\n", colorMood)
	promptContent += fmt.Sprintf("# Generated: %s\n", time.Now().UTC().Format(time.RFC3339))
	promptContent += fmt.Sprintf("# Model: %s\n", result.Model)
	promptContent += fmt.Sprintf("# Source: %s (organized)\n", articlePath)
	promptContent += fmt.Sprintf("# Extracted Elements:\n")
	if len(visualElements.KeyConcepts) > 0 {
		promptContent += fmt.Sprintf("#   Key Concepts: %s\n", strings.Join(visualElements.KeyConcepts, ", "))
	}
	if len(visualElements.SpecificPhenomena) > 0 {
		promptContent += fmt.Sprintf("#   Phenomena: %s\n", strings.Join(visualElements.SpecificPhenomena, ", "))
	}
	if len(visualElements.IconicImagery) > 0 {
		promptContent += fmt.Sprintf("#   Iconic Imagery: %s\n", strings.Join(visualElements.IconicImagery, ", "))
	}
	if len(visualElements.NotableFigures) > 0 {
		promptContent += fmt.Sprintf("#   Notable Figures: %s\n", strings.Join(visualElements.NotableFigures, ", "))
	}
	if len(visualElements.MathElements) > 0 {
		promptContent += fmt.Sprintf("#   Math Elements: %s\n", strings.Join(visualElements.MathElements, ", "))
	}
	if result.Thinking != "" {
		promptContent += fmt.Sprintf("# LLM Reasoning:\n#   %s\n", strings.ReplaceAll(result.Thinking, "\n", "\n#   "))
	}
	promptContent += "\n"
	promptContent += result.Prompt

	if err := a.gh.CreateFile(branchName, promptPath, fmt.Sprintf("Add header image prompt for %s", articleTitle), promptContent); err != nil {
		return fmt.Errorf("failed to save image prompt: %w", err)
	}

	log.Printf("[Image Prompt] Saved prompt to %s (%d chars)", promptPath, len(result.Prompt))

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

// findOrganizedArticle searches for an article in the organized Compendium structure
// Returns the path, domain, category, and topic if found
func (a *Agent) findOrganizedArticle(branchName, articleSlug string) (path, domain, category, topic string) {
	// List domains
	domains, err := a.gh.ListDirectory(branchName, "Compendium")
	if err != nil {
		return "", "", "", ""
	}

	for _, domainDir := range domains {
		if strings.HasPrefix(domainDir, "_") || domainDir == "index.md" {
			continue
		}

		domainPath := filepath.Join("Compendium", domainDir)
		categories, err := a.gh.ListDirectory(branchName, domainPath)
		if err != nil {
			continue
		}

		for _, categoryDir := range categories {
			if strings.HasPrefix(categoryDir, "_") || categoryDir == "index.md" || categoryDir == "img" {
				continue
			}

			categoryPath := filepath.Join(domainPath, categoryDir)
			topics, err := a.gh.ListDirectory(branchName, categoryPath)
			if err != nil {
				continue
			}

			for _, topicDir := range topics {
				if strings.HasPrefix(topicDir, "_") || topicDir == "index.md" || topicDir == "img" {
					continue
				}

				// Check if article exists in this topic
				articlePath := filepath.Join(categoryPath, topicDir, articleSlug+".md")
				if _, _, err := a.gh.GetFile(branchName, articlePath); err == nil {
					return articlePath, domainDir, categoryDir, topicDir
				}
			}
		}
	}

	return "", "", "", ""
}


// imageExistsAnyFormat checks if an image exists with any common extension (.png, .avif, .jpg, .webp)
func (a *Agent) imageExistsAnyFormat(branchName, pathWithoutExt string) bool {
	extensions := []string{".png", ".avif", ".jpg", ".jpeg", ".webp"}
	for _, ext := range extensions {
		if a.fileExists(branchName, pathWithoutExt+ext) {
			return true
		}
	}
	return false
}

// fileExists checks if a file exists at the given path
func (a *Agent) fileExists(branchName, path string) bool {
	_, _, err := a.gh.GetFile(branchName, path)
	return err == nil
}

