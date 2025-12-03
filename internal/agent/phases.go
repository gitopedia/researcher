package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/gitopedia/researcher/internal/kb"
)

// ArticleOutline represents the structure of an article
type ArticleOutline struct {
	Title           string           `json:"title"`
	Summary         string           `json:"summary"`
	Sections        []SectionOutline `json:"sections"`
	TotalWordTarget int              `json:"total_word_target"`
}

// SectionOutline represents a section in the outline
type SectionOutline struct {
	Heading         string           `json:"heading"`
	Level           int              `json:"level"`
	Points          []string         `json:"points"`
	WordTarget      int              `json:"word_target"`
	RelevantSources []int            `json:"relevant_sources"`
	Subsections     []SectionOutline `json:"subsections,omitempty"`
	Content         string           `json:"-"` // Generated content
}

// GapAnalysis represents the result of gap analysis
type GapAnalysis struct {
	Gaps              []Gap              `json:"gaps"`
	SuggestedSections []SuggestedSection `json:"suggested_sections"`
	OverallAssessment string             `json:"overall_assessment"`
}

// Gap represents a gap in the research
type Gap struct {
	Type          string   `json:"type"`
	Section       string   `json:"section,omitempty"`
	Description   string   `json:"description"`
	Priority      string   `json:"priority"`
	SearchQueries []string `json:"search_queries"`
}

// SuggestedSection represents a suggested new section
type SuggestedSection struct {
	Heading         string   `json:"heading"`
	Rationale       string   `json:"rationale"`
	Points          []string `json:"points,omitempty"`
	WordTarget      int      `json:"word_target,omitempty"`
	RelevantSources []int    `json:"relevant_sources,omitempty"`
	Position        string   `json:"position"`
}

// SectionDiscovery represents the result of section discovery
type SectionDiscovery struct {
	SuggestedSections []SuggestedSection `json:"suggested_sections"`
	SkipReason        string             `json:"skip_reason,omitempty"`
}

// SourceInfo holds information about a source for context building
type SourceInfo struct {
	Index   int
	URL     string
	Title   string
	Summary string
}

// PhaseConfig holds configuration for multi-phase generation
type PhaseConfig struct {
	EnableMultiPhase    bool
	MaxResearchRounds   int
	SourcesPerSection   int
	KBClient            *kb.Client
	UseKnowledgeBase    bool
}

// GetPhaseConfig returns the phase configuration from environment
func GetPhaseConfig() PhaseConfig {
	config := PhaseConfig{
		EnableMultiPhase:  os.Getenv("ENABLE_MULTI_PHASE") == "true",
		MaxResearchRounds: 2,
		SourcesPerSection: 8,
		UseKnowledgeBase:  os.Getenv("USE_KNOWLEDGE_BASE") == "true",
	}

	if val := os.Getenv("MAX_RESEARCH_ROUNDS"); val != "" {
		if v, err := strconv.Atoi(val); err == nil && v > 0 {
			config.MaxResearchRounds = v
		}
	}

	if val := os.Getenv("SOURCES_PER_SECTION"); val != "" {
		if v, err := strconv.Atoi(val); err == nil && v > 0 {
			config.SourcesPerSection = v
		}
	}

	if config.UseKnowledgeBase {
		config.KBClient = kb.NewClient()
	}

	return config
}

// Phase1GenerateOutline creates an article outline from sources
func (a *Agent) Phase1GenerateOutline(ctx context.Context, topic string, sources []SourceInfo) (*ArticleOutline, error) {
	log.Printf("[Phase 1] Generating outline for '%s' with %d sources", topic, len(sources))

	// Build sources context
	var sourcesText strings.Builder
	for _, src := range sources {
		sourcesText.WriteString(fmt.Sprintf("[%d] %s\n", src.Index, src.Title))
		sourcesText.WriteString(fmt.Sprintf("    URL: %s\n", src.URL))
		sourcesText.WriteString(fmt.Sprintf("    Summary: %s\n\n", truncate(src.Summary, 500)))
	}

	result, err := a.llm.GenerateOutline(ctx, topic, sourcesText.String())
	if err != nil {
		return nil, fmt.Errorf("outline generation failed: %w", err)
	}

	// Parse JSON response
	var outline ArticleOutline
	if err := json.Unmarshal([]byte(result), &outline); err != nil {
		// Try to extract JSON from response
		jsonStr := extractJSONObject(result)
		if err := json.Unmarshal([]byte(jsonStr), &outline); err != nil {
			return nil, fmt.Errorf("failed to parse outline JSON: %w", err)
		}
	}

	log.Printf("[Phase 1] Generated outline with %d sections, target %d words",
		len(outline.Sections), outline.TotalWordTarget)

	return &outline, nil
}

// Phase2AnalyzeGaps identifies gaps in the research
func (a *Agent) Phase2AnalyzeGaps(ctx context.Context, topic string, outline *ArticleOutline, sources []SourceInfo) (*GapAnalysis, error) {
	log.Printf("[Phase 2] Analyzing gaps for '%s'", topic)

	// Build outline text
	outlineJSON, _ := json.MarshalIndent(outline, "", "  ")

	// Build sources summary
	var sourcesText strings.Builder
	for _, src := range sources {
		sourcesText.WriteString(fmt.Sprintf("[%d] %s: %s\n", src.Index, src.Title, truncate(src.Summary, 200)))
	}

	result, err := a.llm.AnalyzeGaps(ctx, topic, string(outlineJSON), sourcesText.String())
	if err != nil {
		return nil, fmt.Errorf("gap analysis failed: %w", err)
	}

	// Parse JSON response
	var gaps GapAnalysis
	jsonStr := extractJSONObject(result)
	if err := json.Unmarshal([]byte(jsonStr), &gaps); err != nil {
		return nil, fmt.Errorf("failed to parse gap analysis JSON: %w", err)
	}

	log.Printf("[Phase 2] Found %d gaps, %d suggested sections",
		len(gaps.Gaps), len(gaps.SuggestedSections))

	return &gaps, nil
}

// Phase3TargetedResearch performs additional searches to fill gaps
func (a *Agent) Phase3TargetedResearch(ctx context.Context, gaps *GapAnalysis, existingSources []SourceInfo) ([]SourceInfo, error) {
	log.Printf("[Phase 3] Performing targeted research for %d gaps", len(gaps.Gaps))

	var newSources []SourceInfo
	nextIndex := len(existingSources) + 1

	// Process high and medium priority gaps
	for _, gap := range gaps.Gaps {
		if gap.Priority == "low" {
			continue
		}

		for _, query := range gap.SearchQueries {
			log.Printf("[Phase 3] Searching: %s", query)

			results, err := a.search.Search(query)
			if err != nil {
				slog.Warn("Search failed", "query", query, "error", err)
				continue
			}

			// Limit results
			if len(results) > 5 {
				results = results[:5]
			}

			for _, result := range results {
				// Check if we already have this URL
				duplicate := false
				for _, existing := range existingSources {
					if existing.URL == result.Href {
						duplicate = true
						break
					}
				}
				for _, newSrc := range newSources {
					if newSrc.URL == result.Href {
						duplicate = true
						break
					}
				}
				if duplicate {
					continue
				}

				// Fetch and summarize
				content, err := a.search.FetchContent(result.Href)
				if err != nil || len(content) < 500 {
					continue
				}

				summary, err := a.llm.SummarizeSource(ctx, gap.Description, result.Href, content)
				if err != nil || !summary.Relevant {
					continue
				}

				newSources = append(newSources, SourceInfo{
					Index:   nextIndex,
					URL:     result.Href,
					Title:   result.Title,
					Summary: summary.Summary,
				})
				nextIndex++

				// Limit sources per gap
				if len(newSources) >= 3 {
					break
				}
			}
		}
	}

	log.Printf("[Phase 3] Found %d new sources", len(newSources))
	return newSources, nil
}

// Phase4GenerateSections generates content for each section using RAG
func (a *Agent) Phase4GenerateSections(ctx context.Context, topic string, outline *ArticleOutline, sources []SourceInfo, config PhaseConfig) error {
	log.Printf("[Phase 4] Generating %d sections", len(outline.Sections))

	for i := range outline.Sections {
		section := &outline.Sections[i]

		// Select relevant sources for this section
		relevantSources := selectRelevantSources(section, sources, config.SourcesPerSection)

		// Build context from other sections (for coherence)
		var contextText strings.Builder
		for j, other := range outline.Sections {
			if j != i && other.Content != "" {
				contextText.WriteString(fmt.Sprintf("## %s (already written)\n%s\n\n",
					other.Heading, truncate(other.Content, 200)))
			}
		}

		content, err := a.generateSection(ctx, topic, section, relevantSources, contextText.String())
		if err != nil {
			slog.Warn("Failed to generate section", "section", section.Heading, "error", err)
			continue
		}

		section.Content = content
		log.Printf("[Phase 4] Generated section '%s' (%d words)",
			section.Heading, countWords(content))

		// Generate subsections
		for j := range section.Subsections {
			subsection := &section.Subsections[j]
			subContent, err := a.generateSection(ctx, topic, subsection, relevantSources, section.Content)
			if err != nil {
				slog.Warn("Failed to generate subsection", "subsection", subsection.Heading, "error", err)
				continue
			}
			subsection.Content = subContent
		}
	}

	return nil
}

// Phase5DiscoverSections suggests additional sections based on sources
func (a *Agent) Phase5DiscoverSections(ctx context.Context, topic string, outline *ArticleOutline, sources []SourceInfo) (*SectionDiscovery, error) {
	log.Printf("[Phase 5] Discovering additional sections")

	// Build current sections list
	var currentSections strings.Builder
	for _, section := range outline.Sections {
		currentSections.WriteString(fmt.Sprintf("- %s\n", section.Heading))
		for _, sub := range section.Subsections {
			currentSections.WriteString(fmt.Sprintf("  - %s\n", sub.Heading))
		}
	}

	// Build sources summary
	var sourcesText strings.Builder
	for _, src := range sources {
		sourcesText.WriteString(fmt.Sprintf("[%d] %s: %s\n", src.Index, src.Title, truncate(src.Summary, 300)))
	}

	result, err := a.llm.DiscoverSections(ctx, topic, currentSections.String(), sourcesText.String())
	if err != nil {
		return nil, fmt.Errorf("section discovery failed: %w", err)
	}

	// Parse JSON response
	var discovery SectionDiscovery
	jsonStr := extractJSONObject(result)
	if err := json.Unmarshal([]byte(jsonStr), &discovery); err != nil {
		return nil, fmt.Errorf("failed to parse section discovery JSON: %w", err)
	}

	log.Printf("[Phase 5] Discovered %d additional sections", len(discovery.SuggestedSections))
	return &discovery, nil
}

// Phase6IntegrateArticle polishes and integrates all sections
func (a *Agent) Phase6IntegrateArticle(ctx context.Context, topic string, outline *ArticleOutline) (string, error) {
	log.Printf("[Phase 6] Integrating article")

	// Build article from sections
	var article strings.Builder
	for _, section := range outline.Sections {
		if section.Content != "" {
			article.WriteString(section.Content)
			article.WriteString("\n\n")
		}
		for _, sub := range section.Subsections {
			if sub.Content != "" {
				article.WriteString(sub.Content)
				article.WriteString("\n\n")
			}
		}
	}

	// Polish the article
	result, err := a.llm.IntegrateArticle(ctx, topic, article.String())
	if err != nil {
		return "", fmt.Errorf("integration failed: %w", err)
	}

	// Strip code fences if present
	result = stripCodeFences(result)

	log.Printf("[Phase 6] Integrated article (%d words)", countWords(result))
	return result, nil
}

// generateSection generates content for a single section
func (a *Agent) generateSection(ctx context.Context, topic string, section *SectionOutline, sources []SourceInfo, contextText string) (string, error) {
	// Build sources text
	var sourcesText strings.Builder
	for _, src := range sources {
		sourcesText.WriteString(fmt.Sprintf("[%d] %s\n%s\n\n", src.Index, src.Title, src.Summary))
	}

	// Build points text
	var pointsText strings.Builder
	for _, point := range section.Points {
		pointsText.WriteString(fmt.Sprintf("- %s\n", point))
	}

	headingLevel := "##"
	if section.Level == 3 {
		headingLevel = "###"
	}

	result, err := a.llm.GenerateSection(ctx, topic, section.Heading, headingLevel,
		section.WordTarget, pointsText.String(), sourcesText.String(), contextText)
	if err != nil {
		return "", err
	}

	return result, nil
}

// selectRelevantSources selects the most relevant sources for a section
func selectRelevantSources(section *SectionOutline, allSources []SourceInfo, limit int) []SourceInfo {
	var relevant []SourceInfo

	// First, add explicitly marked relevant sources
	for _, idx := range section.RelevantSources {
		for _, src := range allSources {
			if src.Index == idx {
				relevant = append(relevant, src)
				break
			}
		}
	}

	// If not enough, add more based on keyword matching
	if len(relevant) < limit {
		keywords := strings.ToLower(section.Heading)
		for _, point := range section.Points {
			keywords += " " + strings.ToLower(point)
		}

		for _, src := range allSources {
			if len(relevant) >= limit {
				break
			}

			// Check if already added
			found := false
			for _, r := range relevant {
				if r.Index == src.Index {
					found = true
					break
				}
			}
			if found {
				continue
			}

			// Simple keyword matching
			srcText := strings.ToLower(src.Title + " " + src.Summary)
			for _, word := range strings.Fields(keywords) {
				if len(word) > 3 && strings.Contains(srcText, word) {
					relevant = append(relevant, src)
					break
				}
			}
		}
	}

	return relevant
}

// Helper functions

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func countWords(s string) int {
	return len(strings.Fields(s))
}

func extractJSONObject(s string) string {
	// Find first { and last }
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start >= 0 && end > start {
		return s[start : end+1]
	}
	return s
}

