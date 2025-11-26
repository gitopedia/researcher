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
	// Raw contains the raw LLM response used for debugging and logging.
	// It is not part of the JSON contract with the model.
	Raw string `json:"-"`
}

type Generator interface {
	GenerateArticle(ctx context.Context, topic, contextData string) (string, error)
	ExtractEntities(ctx context.Context, content string) ([]ExtractedEntity, error)
	SuggestTopics(ctx context.Context, category string, existingTopics []string) ([]string, error)
	SummarizeSource(ctx context.Context, topic, urlStr, content string) (SourceSummary, error)
}

// Ensure Client implements Generator
var _ Generator = &Client{}
