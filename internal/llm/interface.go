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

// EncyclopediaCheckResult contains the result of checking if a source is an encyclopedia
type EncyclopediaCheckResult struct {
	IsEncyclopedia bool   `json:"is_encyclopedia"`
	Reason         string `json:"reason,omitempty"`
}

// ArticleSection represents a section extracted from an article
type ArticleSection struct {
	Title   string `json:"title"`
	Level   int    `json:"level"`
	Content string `json:"content,omitempty"`
}

// NewSectionSuggestion represents a suggestion for a new section to add
type NewSectionSuggestion struct {
	SectionTitle string `json:"section_title"`
	InsertAfter  string `json:"insert_after"`
	Rationale    string `json:"rationale"`
	SearchQuery  string `json:"search_query"`
}

// SectionComparison represents the result of comparing two articles' sections
type SectionComparison struct {
	HasNewSection  bool           `json:"has_new_section"`
	SectionsToAdd  []SectionToAdd `json:"sections_to_add"`
	SectionTitle   string         `json:"section_title,omitempty"`   // Legacy single section
	SectionContent string         `json:"section_content,omitempty"` // Legacy single section
	InsertAfter    string         `json:"insert_after,omitempty"`
	Reason         string         `json:"reason,omitempty"`
}

// SectionToAdd represents a section to be added to an article
type SectionToAdd struct {
	Title       string `json:"title"`
	Content     string `json:"content"`
	InsertAfter string `json:"insert_after"`
	Reason      string `json:"reason"`
}

// SectionOrderRequest contains input for ordering sections
type SectionOrderRequest struct {
	Topic            string
	ExistingSections []ArticleSection
	NewSections      []SectionToAdd
}

// SectionOrderResult contains the ordered section titles
type SectionOrderResult struct {
	OrderedTitles []string `json:"ordered_titles"`
	Reasoning     string   `json:"reasoning,omitempty"`
}

// SearchQueryResult contains a generated search query
type SearchQueryResult struct {
	SearchQuery string `json:"search_query"`
}

// ImprovementScore represents the quality score of an improvement
type ImprovementScore struct {
	Score          int      `json:"score"`
	Recommendation string   `json:"recommendation"`
	IsImproved     bool     `json:"is_improved"`
	Improvements   []string `json:"improvements,omitempty"`
	Concerns       []string `json:"concerns,omitempty"`
}

// VisualElements contains extracted visual concepts from an article
type VisualElements struct {
	KeyConcepts       []string `json:"key_concepts"`
	SpecificPhenomena []string `json:"specific_phenomena"`
	NotableFigures    []string `json:"notable_figures"`
	IconicImagery     []string `json:"iconic_imagery"`
	MathElements      []string `json:"math_elements"`
}

// VisualElementsRequest contains the input for visual element extraction
type VisualElementsRequest struct {
	Topic          string
	Category       string
	Subcategory    string
	ArticleContent string
}

// ImagePromptRequest contains the input for image prompt generation
type ImagePromptRequest struct {
	Topic             string
	Category          string
	Subcategory       string
	ArticleSummary    string
	ExtractedElements *VisualElements
	ColorMood         string
	ArtisticStyles    []string
	CategoryGuidance  string
}

// ImagePromptResult contains the generated image prompt and metadata
type ImagePromptResult struct {
	Prompt   string
	Model    string
	Thinking string
}

// SectionImageEvaluationRequest contains the input for section image evaluation
type SectionImageEvaluationRequest struct {
	ArticleTitle   string
	SectionTitle   string
	SectionContent string
	Category       string
	Subcategory    string
}

// SectionImageEvaluationResult contains the evaluation scores for different image types
type SectionImageEvaluationResult struct {
	Scores                 map[string]int    `json:"scores"`
	Reasoning              map[string]string `json:"reasoning"`
	RecommendedType        string            `json:"recommended_type"`
	RecommendedScore       int               `json:"recommended_score"`
	KeyElementsToVisualize []string          `json:"key_elements_to_visualize"`
}

// SectionImagePromptRequest contains the input for section image prompt generation
type SectionImagePromptRequest struct {
	ArticleTitle   string
	SectionTitle   string
	SectionContent string
	Category       string
	Subcategory    string
	ImageType      string
	ArtisticStyle  string
	KeyElements    []string
}

// SectionImagePromptResult contains the generated section image prompt
type SectionImagePromptResult struct {
	Prompt string
	Model  string
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
	IsEncyclopediaSource(ctx context.Context, domain, url, title string) (*EncyclopediaCheckResult, error)
	ExtractSections(ctx context.Context, articleContent string) ([]ArticleSection, error)
	SuggestNewSection(ctx context.Context, category, subcategory, topic string, existingSections []ArticleSection) (*NewSectionSuggestion, error)
	CompareSections(ctx context.Context, topic, existingArticle, existingSections, newArticle, newSections string) (*SectionComparison, error)
	OrderSections(ctx context.Context, req SectionOrderRequest) (*SectionOrderResult, error)
	GenerateSectionSearchQuery(ctx context.Context, category, subcategory, topic, sectionTitle, sectionContent string) (*SearchQueryResult, error)
	MergeSection(ctx context.Context, topic, sectionTitle, currentContent, newContent string) (string, error)
	ScoreImprovement(ctx context.Context, topic, sectionTitle, originalContent, improvedContent string) (*ImprovementScore, error)

	// Image generation methods
	ExtractVisualElements(ctx context.Context, req VisualElementsRequest) (*VisualElements, error)
	GenerateImagePrompt(ctx context.Context, req ImagePromptRequest) (*ImagePromptResult, error)
	EvaluateSectionImage(ctx context.Context, req SectionImageEvaluationRequest) (*SectionImageEvaluationResult, error)
	GenerateSectionImagePrompt(ctx context.Context, req SectionImagePromptRequest) (*SectionImagePromptResult, error)
}

// Ensure Client implements Generator
var _ Generator = &Client{}
