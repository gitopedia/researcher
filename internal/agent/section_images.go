package agent

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/gitopedia/researcher/internal/llm"
	"github.com/gitopedia/researcher/internal/styles"
)

// SectionInfo contains information about an article section
type SectionInfo struct {
	Title     string
	Content   string
	StartLine int
	EndLine   int
	WordCount int
	CharCount int
}

// SectionImageConfig holds configuration for section image generation
type SectionImageConfig struct {
	MinWordCount        int
	MinCharCount        int
	GenerationThreshold int
}

// GetSectionImageConfig returns the section image configuration from environment
func GetSectionImageConfig() SectionImageConfig {
	config := SectionImageConfig{
		MinWordCount:        150,
		MinCharCount:        800,
		GenerationThreshold: 60,
	}

	if val := os.Getenv("SECTION_IMAGE_MIN_WORDS"); val != "" {
		if v, err := strconv.Atoi(val); err == nil {
			config.MinWordCount = v
		}
	}
	if val := os.Getenv("SECTION_IMAGE_MIN_CHARS"); val != "" {
		if v, err := strconv.Atoi(val); err == nil {
			config.MinCharCount = v
		}
	}
	if val := os.Getenv("SECTION_IMAGE_THRESHOLD"); val != "" {
		if v, err := strconv.Atoi(val); err == nil {
			config.GenerationThreshold = v
		}
	}

	return config
}

// ExtractSections extracts H2 sections from article markdown content
func ExtractSections(content string) []SectionInfo {
	lines := strings.Split(content, "\n")
	var sections []SectionInfo
	var currentSection *SectionInfo

	// Skip frontmatter
	inFrontmatter := false
	contentStart := 0
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			if !inFrontmatter {
				inFrontmatter = true
			} else {
				contentStart = i + 1
				break
			}
		}
	}

	for i := contentStart; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "## ") {
			// Save previous section
			if currentSection != nil {
				currentSection.EndLine = i - 1
				currentSection.WordCount = countWords(currentSection.Content)
				currentSection.CharCount = len(currentSection.Content)
				sections = append(sections, *currentSection)
			}

			// Start new section
			title := strings.TrimPrefix(trimmed, "## ")
			currentSection = &SectionInfo{
				Title:     title,
				StartLine: i,
				Content:   "",
			}
		} else if currentSection != nil {
			// Add line to current section content
			if currentSection.Content != "" {
				currentSection.Content += "\n"
			}
			currentSection.Content += line
		}
	}

	// Save last section
	if currentSection != nil {
		currentSection.EndLine = len(lines) - 1
		currentSection.WordCount = countWords(currentSection.Content)
		currentSection.CharCount = len(currentSection.Content)
		sections = append(sections, *currentSection)
	}

	return sections
}

// countWords counts words in a string
func countWords(s string) int {
	words := strings.Fields(s)
	return len(words)
}

// EvaluateSectionForImage evaluates if a section should have an image
func (a *Agent) EvaluateSectionForImage(ctx context.Context, articleTitle string, section SectionInfo, domain, category string) (*llm.SectionImageEvaluationResult, error) {
	config := GetSectionImageConfig()

	// Check minimum thresholds
	if section.WordCount < config.MinWordCount || section.CharCount < config.MinCharCount {
		log.Printf("[Section Image] Section '%s' too short (%d words, %d chars)", section.Title, section.WordCount, section.CharCount)
		return nil, nil
	}

	// Evaluate using LLM
	req := llm.SectionImageEvaluationRequest{
		ArticleTitle:   articleTitle,
		SectionTitle:   section.Title,
		SectionContent: section.Content,
		Domain:         domain,
		Category:       category,
	}

	result, err := a.llm.EvaluateSectionImage(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate section: %w", err)
	}

	// Check if recommended type meets threshold
	if result.RecommendedScore < config.GenerationThreshold {
		log.Printf("[Section Image] Section '%s' scored %d (below threshold %d)",
			section.Title, result.RecommendedScore, config.GenerationThreshold)
		return result, nil
	}

	return result, nil
}

// GenerateSectionImagePrompt generates an image prompt for a section
func (a *Agent) GenerateSectionImagePrompt(ctx context.Context, articleTitle string, section SectionInfo, domain, category string, evaluation *llm.SectionImageEvaluationResult) (*llm.SectionImagePromptResult, error) {
	if evaluation == nil || evaluation.RecommendedType == "" || evaluation.RecommendedType == "none" {
		return nil, nil
	}

	config := GetSectionImageConfig()
	if evaluation.RecommendedScore < config.GenerationThreshold {
		return nil, nil
	}

	// Load style configuration
	styleMgr := styles.NewManager("config")
	if err := styleMgr.Load(); err != nil {
		log.Printf("[Section Image] Failed to load styles, using defaults: %v", err)
	}

	// Get styles for this domain/category and image type
	resolved := styleMgr.ResolveAll(evaluation.RecommendedType, strings.ToLower(domain), strings.ToLower(category))
	selectedStyle := ""
	if len(resolved.ArtisticStyles) > 0 {
		selectedStyle = resolved.ArtisticStyles[0]
	}

	// Get the diagram specification for this image type
	diagramSpec := styleMgr.GetDiagramSpecification(evaluation.RecommendedType)

	req := llm.SectionImagePromptRequest{
		ArticleTitle:         articleTitle,
		SectionTitle:         section.Title,
		SectionContent:       section.Content,
		Domain:               domain,
		Category:             category,
		ImageType:            evaluation.RecommendedType,
		ArtisticStyle:        selectedStyle,
		KeyElements:          evaluation.KeyElementsToVisualize,
		DiagramSpecification: diagramSpec,
	}

	return a.llm.GenerateSectionImagePrompt(ctx, req)
}

// ProcessArticleSections processes all sections in an article for image generation
func (a *Agent) ProcessArticleSections(ctx context.Context, branchName, articleName, articleContent, domain, category string) error {
	sections := ExtractSections(articleContent)

	log.Printf("[Section Images] Processing %d sections for %s", len(sections), articleName)

	for _, section := range sections {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Evaluate section
		evaluation, err := a.EvaluateSectionForImage(ctx, articleName, section, domain, category)
		if err != nil {
			log.Printf("[Section Images] Error evaluating section '%s': %v", section.Title, err)
			continue
		}

		if evaluation == nil || evaluation.RecommendedType == "" || evaluation.RecommendedType == "none" {
			continue
		}

		config := GetSectionImageConfig()
		if evaluation.RecommendedScore < config.GenerationThreshold {
			continue
		}

		// Generate prompt
		result, err := a.GenerateSectionImagePrompt(ctx, articleName, section, domain, category, evaluation)
		if err != nil {
			log.Printf("[Section Images] Error generating prompt for section '%s': %v", section.Title, err)
			continue
		}

		if result == nil {
			continue
		}

		// Save the prompt
		sectionSlug := slugify(section.Title)
		promptPath := fmt.Sprintf("Compendium/_debug/articles/%s/section_%s_image_prompt.txt", articleName, sectionSlug)

		promptContent := fmt.Sprintf("# Section Image Prompt for: %s\n", section.Title)
		promptContent += fmt.Sprintf("# Article: %s\n", articleName)
		promptContent += fmt.Sprintf("# Image Type: %s\n", evaluation.RecommendedType)
		promptContent += fmt.Sprintf("# Score: %d\n", evaluation.RecommendedScore)
		promptContent += fmt.Sprintf("# Model: %s\n", result.Model)
		promptContent += "\n"
		promptContent += result.Prompt

		if err := a.gh.CreateFile(branchName, promptPath, fmt.Sprintf("Add section image prompt for %s: %s", articleName, section.Title), promptContent); err != nil {
			log.Printf("[Section Images] Failed to save prompt for section '%s': %v", section.Title, err)
			continue
		}

		log.Printf("[Section Images] Generated prompt for section '%s' (type: %s, score: %d)",
			section.Title, evaluation.RecommendedType, evaluation.RecommendedScore)
	}

	return nil
}

// slugify converts a string to a URL-friendly slug
func slugify(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, " ", "-")
	// Remove non-alphanumeric characters except hyphens
	var result strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			result.WriteRune(r)
		}
	}
	return result.String()
}

