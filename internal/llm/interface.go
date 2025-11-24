package llm

import (
	"context"
)

type Generator interface {
	GenerateArticle(ctx context.Context, topic, contextData string) (string, error)
}

// Ensure Client implements Generator
var _ Generator = &Client{}
