package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/sashabaranov/go-openai"
)

type Client struct {
	client *openai.Client
	model  string
}

func NewClient() *Client {
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

	return &Client{
		client: openai.NewClientWithConfig(config),
		model:  model,
	}
}

func (c *Client) GenerateArticle(ctx context.Context, topic, contextData string) (string, error) {
	prompt := fmt.Sprintf(`
You are an expert encyclopedia author for Gitopedia.
Write a comprehensive article about "%s".

Use the following research context to inform your writing:
%s

**Format Requirements:**
- Use Markdown.
- Start with a YAML front matter block enclosed by "---" lines.
- In the front matter, include:
  - "title": The article title.
  - "summary": A concise 2-3 sentence summary.
  - "tags": A list of relevant topic tags (e.g. ["Technology", "AI"]).
- Do not include "id", "created", or "author" in the front matter (these will be added automatically).
- After the front matter, start the article body with a Level 1 heading (# Title).
- Include an "Overview" section.
- Include a "History" or "Background" section if applicable.
- Cite sources from the context using bracketed numbers like [1], [2].
- At the end, include a "References" section listing the URLs.

**Style:**
- Neutral, objective tone.
- Clear and concise.
`, topic, contextData)

	resp, err := c.client.CreateChatCompletion(
		ctx,
		openai.ChatCompletionRequest{
			Model: c.model,
			Messages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleSystem,
					Content: "You are a helpful and rigorous encyclopedia assistant.",
				},
				{
					Role:    openai.ChatMessageRoleUser,
					Content: prompt,
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
	prompt := fmt.Sprintf(`
Analyze the following article text and extract key entities.
Return the result as a valid JSON array of objects.
Each object should have:
- "name": The exact name of the entity as mentioned.
- "type": One of "person", "org", "place", "topic".

**Text:**
%s

**JSON Output:**
`, content)

	resp, err := c.client.CreateChatCompletion(
		ctx,
		openai.ChatCompletionRequest{
			Model: c.model,
			Messages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleSystem,
					Content: "You are an entity extraction system. You output strictly JSON arrays.",
				},
				{
					Role:    openai.ChatMessageRoleUser,
					Content: prompt,
				},
			},
			Temperature: 0.0, // Deterministic
		},
	)

	if err != nil {
		return nil, err
	}

	jsonStr := resp.Choices[0].Message.Content
	// Clean up markdown code blocks if present
	jsonStr = strings.TrimPrefix(jsonStr, "```json")
	jsonStr = strings.TrimPrefix(jsonStr, "```")
	jsonStr = strings.TrimSuffix(jsonStr, "```")
	jsonStr = strings.TrimSpace(jsonStr)

	var entities []ExtractedEntity
	if err := json.Unmarshal([]byte(jsonStr), &entities); err != nil {
		return nil, fmt.Errorf("failed to parse entities JSON: %w", err)
	}

	return entities, nil
}
