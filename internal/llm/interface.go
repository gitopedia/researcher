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
	Model    string `json:"model,omitempty"`   // Model used for summarization
	// Raw contains the raw LLM response used for debugging and logging.
	// It is not part of the JSON contract with the model.
	Raw string `json:"-"`
	// Step1Output contains the phase 1 step 1 (plain-text summarization) output
	Step1Output string `json:"-"`
}

// ArticleCategory represents the LLM's categorization decision for an article
type ArticleCategory struct {
	Category    string `json:"category"`     // e.g., "Science/Physics"
	Subcategory string `json:"subcategory"`  // e.g., "Quantum Mechanics" (optional)
	Reasoning   string `json:"reasoning"`    // Why this category was chosen
}

// ArticleResult contains the generated article content and metadata
type ArticleResult struct {
	Content  string // The generated article markdown
	Model    string // The model used to generate the article
	Thinking string // The model's reasoning trace (if thinking mode enabled)
}

type Generator interface {
	GenerateArticle(ctx context.Context, topic, contextData string) (*ArticleResult, error)
	ExtractEntities(ctx context.Context, content string) ([]ExtractedEntity, error)
	SuggestTopics(ctx context.Context, category string, existingTopics []string) ([]string, error)
	SummarizeSource(ctx context.Context, topic, urlStr, content string) (SourceSummary, error)
	CategorizeArticle(ctx context.Context, title string, tags []string, content string, existingCategories []string) (*ArticleCategory, error)
}

// Ensure Client implements Generator
var _ Generator = &Client{}
