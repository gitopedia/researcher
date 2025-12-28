package llm

import (
	"context"
)

type EntityType string

const (
	Person EntityType = "person"
	Org    EntityType = "org"
	Place  EntityType = "place"
	Topic  EntityType = "topic"
)

type ExtractedEntity struct {
	Name string     `json:"name"`
	Type EntityType `json:"type"`
}

// SourceSummary represents a summarized view of a single source page.
type SourceSummary struct {
	Relevant bool   `json:"relevant"`
	Reason   string `json:"reason,omitempty"`
	Summary  string `json:"summary"`
	Language string `json:"language,omitempty"` // Detected language code (e.g., "en", "es", "fr")
	Model    string `json:"model,omitempty"`    // Model used for summarization
	// Raw contains the raw LLM response used for debugging and logging.
	// It is not part of the JSON contract with the model.
	Raw string `json:"-"`
	// Step1Output contains the phase 1 step 1 (plain-text summarization) output
	Step1Output string `json:"-"`
}

// ArticleCategory represents the LLM's categorization decision for an article
type ArticleCategory struct {
	Category    string `json:"category"`    // e.g., "Science/Physics"
	Subcategory string `json:"subcategory"` // e.g., "Quantum Mechanics" (optional)
	Reasoning   string `json:"reasoning"`   // Why this category was chosen
}

// ArticleResult contains the generated article content and metadata
type ArticleResult struct {
	Content  string // The generated article markdown
	Model    string // The model used to generate the article
	Thinking string // The model's reasoning trace (if thinking mode enabled)
}

type RelevanceResult struct {
	Relevant bool   `json:"relevant"`
	Reason   string `json:"reason,omitempty"`
}

type RedundancyResult struct {
	IsRedundant bool   `json:"redundant"`
	Reason      string `json:"reason,omitempty"`
}

// EncyclopediaCheckResult indicates whether a source is encyclopedia-like
type EncyclopediaCheckResult struct {
	IsEncyclopedia bool   `json:"is_encyclopedia"`
	Reason         string `json:"reason,omitempty"`
}

// ArticleSection represents a section extracted from an article
type ArticleSection struct {
	Level      int    `json:"level"`       // Heading level (2 for ##, 3 for ###, etc.)
	Title      string `json:"title"`       // Section heading text
	HasContent bool   `json:"has_content"` // Whether section has meaningful content
	Content    string `json:"content,omitempty"` // The actual section content (optional)
}

// SectionToAdd represents a single section that should be added to an article
type SectionToAdd struct {
	Title       string `json:"title"`
	Content     string `json:"content"`
	InsertAfter string `json:"insert_after"`
	Reason      string `json:"reason,omitempty"`
}

// SectionComparisonResult indicates if new sections should be added
type SectionComparisonResult struct {
	HasNewSection  bool           `json:"has_new_section"`
	SectionsToAdd  []SectionToAdd `json:"sections_to_add,omitempty"`
	// Legacy single section fields for backward compatibility
	SectionTitle   string `json:"section_title,omitempty"`
	SectionContent string `json:"section_content,omitempty"`
	InsertAfter    string `json:"insert_after,omitempty"`
	Reason         string `json:"reason,omitempty"`
}

// ImprovementScore evaluates if a section revision is worthwhile
type ImprovementScore struct {
	IsImproved     bool     `json:"is_improved"`
	Score          int      `json:"score"` // 1-10
	Improvements   []string `json:"improvements,omitempty"`
	Concerns       []string `json:"concerns,omitempty"`
	Recommendation string   `json:"recommendation"` // "accept" or "reject"
}

type Generator interface {
	GenerateArticle(ctx context.Context, topic, contextData string) (*ArticleResult, error)
	AddReferences(ctx context.Context, article string, sources string) (string, error)
	ExtractEntities(ctx context.Context, content string) ([]ExtractedEntity, error)
	SuggestTopics(ctx context.Context, category string, existingTopics []string) ([]string, error)
	SummarizeSource(ctx context.Context, topic, urlStr, content string) (SourceSummary, error)
	CategorizeArticle(ctx context.Context, title string, tags []string, content string, existingCategories []string) (*ArticleCategory, error)

	// Incremental workflow methods
	GenerateMiniArticle(ctx context.Context, topic, sourceTitle, sourceSummary string) (string, error)
	CheckRelevance(ctx context.Context, topic, content string) (*RelevanceResult, error)
	CheckRedundancy(ctx context.Context, topic, existingArticle, newContent string) (*RedundancyResult, error)
	IntegrateContent(ctx context.Context, topic, existingArticle, newContent string) (string, error)

	// Article improvement methods
	IsEncyclopediaSource(ctx context.Context, domain, url, title string) (*EncyclopediaCheckResult, error)
	ExtractSections(ctx context.Context, article string) ([]ArticleSection, error)
	CompareSections(ctx context.Context, topic, existingArticle, existingSections, newArticle, newSections string) (*SectionComparisonResult, error)
	MergeSection(ctx context.Context, topic, sectionTitle, currentSection, newContent string) (string, error)
	ScoreImprovement(ctx context.Context, topic, sectionTitle, originalSection, revisedSection string) (*ImprovementScore, error)
}

// Ensure Client implements Generator
var _ Generator = &Client{}
