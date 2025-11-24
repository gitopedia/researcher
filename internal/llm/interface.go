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

type Generator interface {
	GenerateArticle(ctx context.Context, topic, contextData string) (string, error)
	ExtractEntities(ctx context.Context, content string) ([]ExtractedEntity, error)
	SuggestTopics(ctx context.Context, category string, existingTopics []string) ([]string, error)
}

// Ensure Client implements Generator
var _ Generator = &Client{}
