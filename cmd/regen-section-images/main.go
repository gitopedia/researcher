// Command regen-section-images regenerates section image prompts for articles
// using the updated diagram specification system, and optionally generates the images.
//
// Usage:
//
//	go run ./cmd/regen-section-images --repo-path ../gitopedia --all
//	go run ./cmd/regen-section-images --article quantum-decoherence --repo-path ../gitopedia
//	go run ./cmd/regen-section-images --repo-path ../gitopedia --all --generate-images
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gitopedia/researcher/internal/agent"
	"github.com/gitopedia/researcher/internal/comfyui"
	"github.com/gitopedia/researcher/internal/llm"
	"github.com/gitopedia/researcher/internal/styles"
)

func main() {
	articleSlug := flag.String("article", "", "Article slug (e.g., quantum-decoherence)")
	repoPath := flag.String("repo-path", "../gitopedia", "Path to gitopedia repository")
	domain := flag.String("domain", "Science", "Article domain (used when --article is specified)")
	category := flag.String("category", "Physics", "Article category (used when --article is specified)")
	outputDir := flag.String("output", "", "Output directory (defaults to repo _debug folder)")
	dryRun := flag.Bool("dry-run", false, "Print prompts to stdout instead of writing files")
	processAll := flag.Bool("all", false, "Process all articles in the _incoming folder")
	generateImages := flag.Bool("generate-images", false, "Generate images after creating prompts")
	comfyURL := flag.String("comfyui-url", "http://localhost:8188", "ComfyUI API URL")
	flag.Parse()

	if *articleSlug == "" && !*processAll {
		log.Fatal("Either --article or --all is required")
	}

	// Initialize LLM client
	llmClient, err := llm.NewClient()
	if err != nil {
		log.Fatalf("Failed to create LLM client: %v", err)
	}

	// Load style configuration
	configDir := "config"
	if _, err := os.Stat(configDir); os.IsNotExist(err) {
		configDir = filepath.Join(filepath.Dir(os.Args[0]), "..", "..", "config")
	}

	styleMgr := styles.NewManager(configDir)
	if err := styleMgr.Load(); err != nil {
		log.Fatalf("Failed to load style configuration: %v", err)
	}

	ctx := context.Background()
	var articlesToProcess []articleInfo

	if *processAll {
		// Find all articles in _incoming
		incomingPath := filepath.Join(*repoPath, "Compendium", "_incoming")
		entries, err := os.ReadDir(incomingPath)
		if err != nil {
			log.Fatalf("Failed to read incoming directory: %v", err)
		}

		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}
			slug := strings.TrimSuffix(entry.Name(), ".md")
			articlePath := filepath.Join(incomingPath, entry.Name())

			// Read article to extract category
			content, err := os.ReadFile(articlePath)
			if err != nil {
				log.Printf("Warning: Could not read %s: %v", articlePath, err)
				continue
			}

			dom, cat := extractCategoryFromFrontmatter(string(content))
			articlesToProcess = append(articlesToProcess, articleInfo{
				slug:     slug,
				path:     articlePath,
				domain:   dom,
				category: cat,
			})
		}
		log.Printf("Found %d articles to process", len(articlesToProcess))
	} else {
		// Single article mode
		articlePath := filepath.Join(*repoPath, "Compendium", "_incoming", *articleSlug+".md")
		if _, err := os.Stat(articlePath); os.IsNotExist(err) {
			articlePath = filepath.Join(*repoPath, "Compendium", "articles", *articleSlug+".md")
		}
		articlesToProcess = append(articlesToProcess, articleInfo{
			slug:     *articleSlug,
			path:     articlePath,
			domain:   *domain,
			category: *category,
		})
	}

	// Track generated prompts for image generation
	var generatedPrompts []promptInfo

	// Process each article
	for _, article := range articlesToProcess {
		log.Printf("\n========================================")
		log.Printf("Processing article: %s", article.slug)
		log.Printf("========================================")

		prompts, err := processArticle(ctx, article, llmClient, styleMgr, *repoPath, *outputDir, *dryRun)
		if err != nil {
			log.Printf("Error processing %s: %v", article.slug, err)
			continue
		}
		generatedPrompts = append(generatedPrompts, prompts...)
	}

	log.Printf("\n========================================")
	log.Printf("Prompt generation complete: %d prompts created", len(generatedPrompts))
	log.Printf("========================================")

	// Generate images if requested
	if *generateImages && len(generatedPrompts) > 0 && !*dryRun {
		log.Printf("\nStarting image generation...")

		comfyClient := comfyui.NewClient(*comfyURL)

		// Check if ComfyUI is running
		if !comfyClient.IsHealthy(ctx) {
			log.Fatal("ComfyUI is not running. Please start it first.")
		}

		log.Printf("ComfyUI is healthy, generating %d images...", len(generatedPrompts))

		for i, p := range generatedPrompts {
			log.Printf("\n[%d/%d] Generating image for: %s - %s", i+1, len(generatedPrompts), p.articleSlug, p.sectionSlug)

			opts := comfyui.DefaultOptions()
			opts.Width = 1024
			opts.Height = 768

			imageData, err := comfyClient.GenerateImage(ctx, p.promptText, &opts)
			if err != nil {
				log.Printf("  ERROR: %v", err)
				continue
			}

			// Save the image
			imagePath := filepath.Join(*repoPath, "Compendium", "_incoming",
				fmt.Sprintf("%s_section_%s.png", p.articleSlug, p.sectionSlug))

			if err := os.WriteFile(imagePath, imageData, 0644); err != nil {
				log.Printf("  ERROR saving image: %v", err)
				continue
			}

			log.Printf("  SAVED: %s (%d bytes)", imagePath, len(imageData))
		}

		log.Printf("\nImage generation complete!")
	}

	log.Println("\nDone!")
}

type articleInfo struct {
	slug     string
	path     string
	domain   string
	category string
}

type promptInfo struct {
	articleSlug string
	sectionSlug string
	promptText  string
	outputPath  string
}

func processArticle(ctx context.Context, article articleInfo, llmClient *llm.Client, styleMgr *styles.Manager, repoPath, outputDir string, dryRun bool) ([]promptInfo, error) {
	articleContent, err := os.ReadFile(article.path)
	if err != nil {
		return nil, fmt.Errorf("failed to read article: %w", err)
	}

	log.Printf("Read article: %s (%d bytes)", article.path, len(articleContent))

	// Extract sections
	sections := agent.ExtractSections(string(articleContent))
	log.Printf("Found %d sections", len(sections))

	config := agent.GetSectionImageConfig()

	// Determine output directory
	outDir := outputDir
	if outDir == "" {
		outDir = filepath.Join(repoPath, "Compendium", "_debug", "articles", article.slug)
	}
	if !dryRun {
		if err := os.MkdirAll(outDir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create output directory: %w", err)
		}
	}

	var generatedPrompts []promptInfo

	// Process each section
	for _, section := range sections {
		log.Printf("\n--- Section: %s ---", section.Title)
		log.Printf("  Words: %d, Chars: %d", section.WordCount, section.CharCount)

		// Check minimum thresholds
		if section.WordCount < config.MinWordCount || section.CharCount < config.MinCharCount {
			log.Printf("  SKIPPED: Too short (min: %d words, %d chars)", config.MinWordCount, config.MinCharCount)
			continue
		}

		// Evaluate section for image suitability
		evalReq := llm.SectionImageEvaluationRequest{
			ArticleTitle:   article.slug,
			SectionTitle:   section.Title,
			SectionContent: section.Content,
			Domain:         article.domain,
			Category:       article.category,
		}

		startTime := time.Now()
		evaluation, err := llmClient.EvaluateSectionImage(ctx, evalReq)
		if err != nil {
			log.Printf("  ERROR evaluating: %v", err)
			continue
		}

		log.Printf("  Evaluation (%v): type=%s, score=%d", time.Since(startTime).Round(time.Second), evaluation.RecommendedType, evaluation.RecommendedScore)
		log.Printf("  Key elements: %v", evaluation.KeyElementsToVisualize)

		if evaluation.RecommendedScore < config.GenerationThreshold {
			log.Printf("  SKIPPED: Score below threshold (%d < %d)", evaluation.RecommendedScore, config.GenerationThreshold)
			continue
		}

		if evaluation.RecommendedType == "" || evaluation.RecommendedType == "none" {
			log.Printf("  SKIPPED: No recommended type")
			continue
		}

		// Get styles and diagram specification
		resolved := styleMgr.ResolveAll(evaluation.RecommendedType, strings.ToLower(article.domain), strings.ToLower(article.category))
		selectedStyle := ""
		if len(resolved.ArtisticStyles) > 0 {
			selectedStyle = resolved.ArtisticStyles[0]
		}

		diagramSpec := styleMgr.GetDiagramSpecification(evaluation.RecommendedType)

		log.Printf("  Style: %s", selectedStyle)
		log.Printf("  Diagram spec loaded: %d chars", len(diagramSpec))

		// Generate the prompt
		promptReq := llm.SectionImagePromptRequest{
			ArticleTitle:         article.slug,
			SectionTitle:         section.Title,
			SectionContent:       section.Content,
			Domain:               article.domain,
			Category:             article.category,
			ImageType:            evaluation.RecommendedType,
			ArtisticStyle:        selectedStyle,
			KeyElements:          evaluation.KeyElementsToVisualize,
			DiagramSpecification: diagramSpec,
		}

		startTime = time.Now()
		result, err := llmClient.GenerateSectionImagePrompt(ctx, promptReq)
		if err != nil {
			log.Printf("  ERROR generating prompt: %v", err)
			continue
		}

		log.Printf("  Generated prompt in %v (%d chars)", time.Since(startTime).Round(time.Second), len(result.Prompt))

		// Output the result
		sectionSlug := slugify(section.Title)
		promptContent := fmt.Sprintf("# Section Image Prompt for: %s\n", section.Title)
		promptContent += fmt.Sprintf("# Article: %s\n", article.slug)
		promptContent += fmt.Sprintf("# Image Type: %s\n", evaluation.RecommendedType)
		promptContent += fmt.Sprintf("# Score: %d\n", evaluation.RecommendedScore)
		promptContent += fmt.Sprintf("# Model: %s\n", result.Model)
		promptContent += "\n"
		promptContent += result.Prompt

		if dryRun {
			fmt.Printf("\n%s\n", strings.Repeat("=", 80))
			fmt.Printf("FILE: section_%s_image_prompt.txt\n", sectionSlug)
			fmt.Printf("%s\n", strings.Repeat("=", 80))
			fmt.Println(promptContent)
		} else {
			outPath := filepath.Join(outDir, fmt.Sprintf("section_%s_image_prompt.txt", sectionSlug))
			if err := os.WriteFile(outPath, []byte(promptContent), 0644); err != nil {
				log.Printf("  ERROR writing file: %v", err)
				continue
			}
			log.Printf("  WRITTEN: %s", outPath)
		}

		generatedPrompts = append(generatedPrompts, promptInfo{
			articleSlug: article.slug,
			sectionSlug: sectionSlug,
			promptText:  result.Prompt,
			outputPath:  filepath.Join(outDir, fmt.Sprintf("section_%s_image_prompt.txt", sectionSlug)),
		})
	}

	return generatedPrompts, nil
}

func slugify(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, " ", "-")
	var result strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			result.WriteRune(r)
		}
	}
	return result.String()
}

func extractCategoryFromFrontmatter(content string) (domain, category string) {
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

		if strings.HasPrefix(trimmed, "domain:") {
			domain = strings.TrimSpace(strings.TrimPrefix(trimmed, "domain:"))
			domain = strings.Trim(domain, "\"'")
		}
		if strings.HasPrefix(trimmed, "category:") {
			category = strings.TrimSpace(strings.TrimPrefix(trimmed, "category:"))
			category = strings.Trim(category, "\"'")
		}
	}

	if domain == "" {
		domain = "Science"
	}
	if category == "" {
		category = "Physics"
	}

	return domain, category
}
