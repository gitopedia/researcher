package queue

import (
	"context"

	"github.com/gitopedia/researcher/internal/llm"
)

// QueuedLLMClient wraps an llm.Generator and routes every call through the
// LLM queue. It implements llm.Generator so it can be used as a drop-in
// replacement wherever the original client is used.
type QueuedLLMClient struct {
	inner llm.Generator
	q     *Queue
}

// Ensure interface compliance at compile time.
var _ llm.Generator = (*QueuedLLMClient)(nil)

// NewQueuedLLMClient creates a new queued LLM client.
func NewQueuedLLMClient(inner llm.Generator, q *Queue) *QueuedLLMClient {
	return &QueuedLLMClient{inner: inner, q: q}
}

func (c *QueuedLLMClient) GenerateArticle(ctx context.Context, topic, contextData string) (*llm.ArticleResult, error) {
	return SubmitLLM(ctx, c.q, "GenerateArticle:"+topic, func(ctx context.Context) (*llm.ArticleResult, error) {
		return c.inner.GenerateArticle(ctx, topic, contextData)
	})
}

func (c *QueuedLLMClient) AddReferences(ctx context.Context, article string, sources string) (string, error) {
	return SubmitLLM(ctx, c.q, "AddReferences", func(ctx context.Context) (string, error) {
		return c.inner.AddReferences(ctx, article, sources)
	})
}

func (c *QueuedLLMClient) SuggestTopics(ctx context.Context, category string, existingTopics []string) ([]string, error) {
	return SubmitLLM(ctx, c.q, "SuggestTopics:"+category, func(ctx context.Context) ([]string, error) {
		return c.inner.SuggestTopics(ctx, category, existingTopics)
	})
}

func (c *QueuedLLMClient) SummarizeSource(ctx context.Context, topic, urlStr, content string) (llm.SourceSummary, error) {
	return SubmitLLM(ctx, c.q, "SummarizeSource:"+topic, func(ctx context.Context) (llm.SourceSummary, error) {
		return c.inner.SummarizeSource(ctx, topic, urlStr, content)
	})
}

func (c *QueuedLLMClient) CategorizeArticle(ctx context.Context, title string, tags []string, content string, existingCategories []string) (*llm.ArticleCategory, error) {
	return SubmitLLM(ctx, c.q, "CategorizeArticle:"+title, func(ctx context.Context) (*llm.ArticleCategory, error) {
		return c.inner.CategorizeArticle(ctx, title, tags, content, existingCategories)
	})
}

func (c *QueuedLLMClient) GenerateMiniArticle(ctx context.Context, topic, sourceTitle, sourceSummary string) (string, error) {
	return SubmitLLM(ctx, c.q, "GenerateMiniArticle:"+topic, func(ctx context.Context) (string, error) {
		return c.inner.GenerateMiniArticle(ctx, topic, sourceTitle, sourceSummary)
	})
}

func (c *QueuedLLMClient) CheckRelevance(ctx context.Context, topic, content string) (*llm.RelevanceResult, error) {
	return SubmitLLM(ctx, c.q, "CheckRelevance:"+topic, func(ctx context.Context) (*llm.RelevanceResult, error) {
		return c.inner.CheckRelevance(ctx, topic, content)
	})
}

func (c *QueuedLLMClient) CheckRedundancy(ctx context.Context, topic, existingArticle, newContent string) (*llm.RedundancyResult, error) {
	return SubmitLLM(ctx, c.q, "CheckRedundancy:"+topic, func(ctx context.Context) (*llm.RedundancyResult, error) {
		return c.inner.CheckRedundancy(ctx, topic, existingArticle, newContent)
	})
}

func (c *QueuedLLMClient) IntegrateContent(ctx context.Context, topic, existingArticle, newContent string) (string, error) {
	return SubmitLLM(ctx, c.q, "IntegrateContent:"+topic, func(ctx context.Context) (string, error) {
		return c.inner.IntegrateContent(ctx, topic, existingArticle, newContent)
	})
}

func (c *QueuedLLMClient) IsEncyclopediaSource(ctx context.Context, domain, url, title string) (*llm.EncyclopediaCheckResult, error) {
	return SubmitLLM(ctx, c.q, "IsEncyclopediaSource", func(ctx context.Context) (*llm.EncyclopediaCheckResult, error) {
		return c.inner.IsEncyclopediaSource(ctx, domain, url, title)
	})
}

func (c *QueuedLLMClient) ExtractSections(ctx context.Context, articleContent string) ([]llm.ArticleSection, error) {
	return SubmitLLM(ctx, c.q, "ExtractSections", func(ctx context.Context) ([]llm.ArticleSection, error) {
		return c.inner.ExtractSections(ctx, articleContent)
	})
}

func (c *QueuedLLMClient) SuggestNewSection(ctx context.Context, domain, category, topic string, existingSections []llm.ArticleSection) (*llm.NewSectionSuggestion, error) {
	return SubmitLLM(ctx, c.q, "SuggestNewSection:"+topic, func(ctx context.Context) (*llm.NewSectionSuggestion, error) {
		return c.inner.SuggestNewSection(ctx, domain, category, topic, existingSections)
	})
}

func (c *QueuedLLMClient) CompareSections(ctx context.Context, topic, existingArticle, existingSections, newArticle, newSections string) (*llm.SectionComparison, error) {
	return SubmitLLM(ctx, c.q, "CompareSections:"+topic, func(ctx context.Context) (*llm.SectionComparison, error) {
		return c.inner.CompareSections(ctx, topic, existingArticle, existingSections, newArticle, newSections)
	})
}

func (c *QueuedLLMClient) OrderSections(ctx context.Context, req llm.SectionOrderRequest) (*llm.SectionOrderResult, error) {
	return SubmitLLM(ctx, c.q, "OrderSections", func(ctx context.Context) (*llm.SectionOrderResult, error) {
		return c.inner.OrderSections(ctx, req)
	})
}

func (c *QueuedLLMClient) MergeSection(ctx context.Context, topic, sectionTitle, currentContent, newContent string) (string, error) {
	return SubmitLLM(ctx, c.q, "MergeSection:"+topic, func(ctx context.Context) (string, error) {
		return c.inner.MergeSection(ctx, topic, sectionTitle, currentContent, newContent)
	})
}

func (c *QueuedLLMClient) ScoreImprovement(ctx context.Context, topic, sectionTitle, originalContent, improvedContent string) (*llm.ImprovementScore, error) {
	return SubmitLLM(ctx, c.q, "ScoreImprovement:"+topic, func(ctx context.Context) (*llm.ImprovementScore, error) {
		return c.inner.ScoreImprovement(ctx, topic, sectionTitle, originalContent, improvedContent)
	})
}

func (c *QueuedLLMClient) ExtractConcepts(ctx context.Context, topic, article, sourceSummary string) (*llm.ConceptExtraction, error) {
	return SubmitLLM(ctx, c.q, "ExtractConcepts:"+topic, func(ctx context.Context) (*llm.ConceptExtraction, error) {
		return c.inner.ExtractConcepts(ctx, topic, article, sourceSummary)
	})
}

func (c *QueuedLLMClient) MapConceptToSection(ctx context.Context, topic string, sections []string, concept llm.ExtractedConcept) (*llm.SectionMapping, error) {
	return SubmitLLM(ctx, c.q, "MapConceptToSection:"+topic, func(ctx context.Context) (*llm.SectionMapping, error) {
		return c.inner.MapConceptToSection(ctx, topic, sections, concept)
	})
}

func (c *QueuedLLMClient) RewriteSectionWithConcept(ctx context.Context, topic, sectionContent string, concept llm.ExtractedConcept) (string, error) {
	return SubmitLLM(ctx, c.q, "RewriteSectionWithConcept:"+topic, func(ctx context.Context) (string, error) {
		return c.inner.RewriteSectionWithConcept(ctx, topic, sectionContent, concept)
	})
}

func (c *QueuedLLMClient) GenerateNewSection(ctx context.Context, topic string, concept llm.ExtractedConcept, headingLevel int, existingArticle string) (string, error) {
	return SubmitLLM(ctx, c.q, "GenerateNewSection:"+topic, func(ctx context.Context) (string, error) {
		return c.inner.GenerateNewSection(ctx, topic, concept, headingLevel, existingArticle)
	})
}

func (c *QueuedLLMClient) ExtractVisualElements(ctx context.Context, req llm.VisualElementsRequest) (*llm.VisualElements, error) {
	return SubmitLLM(ctx, c.q, "ExtractVisualElements", func(ctx context.Context) (*llm.VisualElements, error) {
		return c.inner.ExtractVisualElements(ctx, req)
	})
}

func (c *QueuedLLMClient) GenerateImagePrompt(ctx context.Context, req llm.ImagePromptRequest) (*llm.ImagePromptResult, error) {
	return SubmitLLM(ctx, c.q, "GenerateImagePrompt", func(ctx context.Context) (*llm.ImagePromptResult, error) {
		return c.inner.GenerateImagePrompt(ctx, req)
	})
}

func (c *QueuedLLMClient) EvaluateSectionImage(ctx context.Context, req llm.SectionImageEvaluationRequest) (*llm.SectionImageEvaluationResult, error) {
	return SubmitLLM(ctx, c.q, "EvaluateSectionImage", func(ctx context.Context) (*llm.SectionImageEvaluationResult, error) {
		return c.inner.EvaluateSectionImage(ctx, req)
	})
}

func (c *QueuedLLMClient) GenerateSectionImagePrompt(ctx context.Context, req llm.SectionImagePromptRequest) (*llm.SectionImagePromptResult, error) {
	return SubmitLLM(ctx, c.q, "GenerateSectionImagePrompt", func(ctx context.Context) (*llm.SectionImagePromptResult, error) {
		return c.inner.GenerateSectionImagePrompt(ctx, req)
	})
}

func (c *QueuedLLMClient) GenerateIndexImagePrompt(ctx context.Context, req llm.IndexImagePromptRequest) (*llm.IndexImagePromptResult, error) {
	return SubmitLLM(ctx, c.q, "GenerateIndexImagePrompt", func(ctx context.Context) (*llm.IndexImagePromptResult, error) {
		return c.inner.GenerateIndexImagePrompt(ctx, req)
	})
}
