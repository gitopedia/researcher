package llm

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/template"

	"github.com/sashabaranov/go-openai"
)

//go:embed prompts/*.txt
var promptsFS embed.FS

type Client struct {
	client                           *openai.Client
	model                            string
	generateArticleSystemTemplate    *template.Template
	generateArticleUserTemplate      *template.Template
	extractEntitiesSystemTemplate    *template.Template
	extractEntitiesUserTemplate      *template.Template
	suggestTopicsSystemTemplate      *template.Template
	suggestTopicsUserTemplate        *template.Template
	summarizeSourceSystemTemplate    *template.Template
	summarizeSourceUserTemplate      *template.Template
}

func NewClient() (*Client, error) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	baseUrl := os.Getenv("OPENAI_BASE_URL")
	model := os.Getenv("OPENAI_MODEL")

	if model == "" {
		model = openai.GPT3Dot5Turbo
	}

	config := openai.DefaultConfig(apiKey)
	if baseUrl != "" {
		config.BaseURL = baseUrl
	}

	// Load prompt templates
	generateArticleSystem, err := loadTemplate("prompts/generate_article_system.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load generate_article_system template: %w", err)
	}

	generateArticleUser, err := loadTemplate("prompts/generate_article_user.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load generate_article_user template: %w", err)
	}

	extractEntitiesSystem, err := loadTemplate("prompts/extract_entities_system.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load extract_entities_system template: %w", err)
	}

	extractEntitiesUser, err := loadTemplate("prompts/extract_entities_user.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load extract_entities_user template: %w", err)
	}

	suggestTopicsSystem, err := loadTemplate("prompts/suggest_topics_system.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load suggest_topics_system template: %w", err)
	}

	suggestTopicsUser, err := loadTemplate("prompts/suggest_topics_user.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load suggest_topics_user template: %w", err)
	}

	summarizeSourceSystem, err := loadTemplate("prompts/summarize_source_system.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load summarize_source_system template: %w", err)
	}

	summarizeSourceUser, err := loadTemplate("prompts/summarize_source_user.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load summarize_source_user template: %w", err)
	}

	return &Client{
		client:                        openai.NewClientWithConfig(config),
		model:                         model,
		generateArticleSystemTemplate: generateArticleSystem,
		generateArticleUserTemplate:   generateArticleUser,
		extractEntitiesSystemTemplate: extractEntitiesSystem,
		extractEntitiesUserTemplate:   extractEntitiesUser,
		suggestTopicsSystemTemplate:   suggestTopicsSystem,
		suggestTopicsUserTemplate:     suggestTopicsUser,
		summarizeSourceSystemTemplate: summarizeSourceSystem,
		summarizeSourceUserTemplate:   summarizeSourceUser,
	}, nil
}

func loadTemplate(path string) (*template.Template, error) {
	content, err := promptsFS.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return template.New(path).Parse(string(content))
}

func (c *Client) GenerateArticle(ctx context.Context, topic, contextData string) (string, error) {
	// Execute system template
	var systemBuf bytes.Buffer
	if err := c.generateArticleSystemTemplate.Execute(&systemBuf, nil); err != nil {
		return "", fmt.Errorf("failed to execute system template: %w", err)
	}

	// Execute user template
	data := map[string]interface{}{
		"Topic":       topic,
		"ContextData": contextData,
	}
	var userBuf bytes.Buffer
	if err := c.generateArticleUserTemplate.Execute(&userBuf, data); err != nil {
		return "", fmt.Errorf("failed to execute user template: %w", err)
	}

	resp, err := c.client.CreateChatCompletion(
		ctx,
		openai.ChatCompletionRequest{
			Model: c.model,
			Messages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleSystem,
					Content: systemBuf.String(),
				},
				{
					Role:    openai.ChatMessageRoleUser,
					Content: userBuf.String(),
				},
			},
			Temperature: 0.7,
		},
	)

	if err != nil {
		return "", err
	}

	return resp.Choices[0].Message.Content, nil
}

func (c *Client) ExtractEntities(ctx context.Context, content string) ([]ExtractedEntity, error) {
	// Execute system template
	var systemBuf bytes.Buffer
	if err := c.extractEntitiesSystemTemplate.Execute(&systemBuf, nil); err != nil {
		return nil, fmt.Errorf("failed to execute system template: %w", err)
	}

	// Execute user template
	data := map[string]interface{}{
		"Content": content,
	}
	var userBuf bytes.Buffer
	if err := c.extractEntitiesUserTemplate.Execute(&userBuf, data); err != nil {
		return nil, fmt.Errorf("failed to execute user template: %w", err)
	}

	resp, err := c.client.CreateChatCompletion(
		ctx,
		openai.ChatCompletionRequest{
			Model: c.model,
			Messages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleSystem,
					Content: systemBuf.String(),
				},
				{
					Role:    openai.ChatMessageRoleUser,
					Content: userBuf.String(),
				},
			},
			Temperature: 0.0, // Deterministic
		},
	)

	if err != nil {
		return nil, err
	}

	jsonStr := extractJSONArray(resp.Choices[0].Message.Content)

	var entities []ExtractedEntity
	if err := json.Unmarshal([]byte(jsonStr), &entities); err != nil {
		return nil, fmt.Errorf("failed to parse entities JSON: %w (input: %q)", err, jsonStr)
	}

	return entities, nil
}

func (c *Client) SuggestTopics(ctx context.Context, category string, existingTopics []string) ([]string, error) {
	// Execute system template
	var systemBuf bytes.Buffer
	if err := c.suggestTopicsSystemTemplate.Execute(&systemBuf, nil); err != nil {
		return nil, fmt.Errorf("failed to execute system template: %w", err)
	}

	// Execute user template
	data := map[string]interface{}{
		"Category":       category,
		"ExistingTopics": strings.Join(existingTopics, ", "),
	}
	var userBuf bytes.Buffer
	if err := c.suggestTopicsUserTemplate.Execute(&userBuf, data); err != nil {
		return nil, fmt.Errorf("failed to execute user template: %w", err)
	}

	resp, err := c.client.CreateChatCompletion(
		ctx,
		openai.ChatCompletionRequest{
			Model: c.model,
			Messages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleSystem,
					Content: systemBuf.String(),
				},
				{
					Role:    openai.ChatMessageRoleUser,
					Content: userBuf.String(),
				},
			},
			Temperature: 0.8,
		},
	)

	if err != nil {
		return nil, err
	}

	jsonStr := extractJSONArray(resp.Choices[0].Message.Content)

	var topics []string
	if err := json.Unmarshal([]byte(jsonStr), &topics); err != nil {
		return nil, fmt.Errorf("failed to parse topics JSON: %w (input: %q)", err, jsonStr)
	}

	return topics, nil
}

// SummarizeSource compresses a single source page into a focused summary and
// decides whether it is relevant enough to include.
func (c *Client) SummarizeSource(ctx context.Context, topic, urlStr, content string) (SourceSummary, error) {
	// Execute system template
	var systemBuf bytes.Buffer
	if err := c.summarizeSourceSystemTemplate.Execute(&systemBuf, nil); err != nil {
		return SourceSummary{}, fmt.Errorf("failed to execute summarize system template: %w", err)
	}

	// Execute user template
	data := map[string]interface{}{
		"Topic":   topic,
		"URL":     urlStr,
		"Content": content,
	}
	var userBuf bytes.Buffer
	if err := c.summarizeSourceUserTemplate.Execute(&userBuf, data); err != nil {
		return SourceSummary{}, fmt.Errorf("failed to execute summarize user template: %w", err)
	}

	resp, err := c.client.CreateChatCompletion(
		ctx,
		openai.ChatCompletionRequest{
			Model: c.model,
			Messages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleSystem,
					Content: systemBuf.String(),
				},
				{
					Role:    openai.ChatMessageRoleUser,
					Content: userBuf.String(),
				},
			},
			Temperature: 0.1, // keep summarization highly deterministic for JSON output
		},
	)
	if err != nil {
		return SourceSummary{}, err
	}

	raw := strings.TrimSpace(resp.Choices[0].Message.Content)
	// Extract JSON object from response (in case LLM adds extra text)
	jsonStr := extractJSONObject(raw)

	var summary SourceSummary
	summary.Raw = raw
	if err := json.Unmarshal([]byte(jsonStr), &summary); err != nil {
		// Return summary with Raw populated so callers can debug, but still surface error.
		return summary, fmt.Errorf("failed to parse source summary JSON: %w (input: %q)", err, raw)
	}

	return summary, nil
}

// extractJSONArray finds the first '[' and last ']' to extract the JSON array.
func extractJSONArray(s string) string {
	start := strings.Index(s, "[")
	end := strings.LastIndex(s, "]")
	if start == -1 || end == -1 || start > end {
		return s // Return original if pattern not found
	}
	return s[start : end+1]
}

// extractJSONObject finds the first '{' and matching '}' to extract the JSON object.
func extractJSONObject(s string) string {
	start := strings.Index(s, "{")
	if start == -1 {
		return s // Return original if no opening brace found
	}
	
	// Find matching closing brace by counting braces
	depth := 0
	for i := start; i < len(s); i++ {
		if s[i] == '{' {
			depth++
		} else if s[i] == '}' {
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	
	// If no matching brace found, return from start to end
	return s[start:]
}
