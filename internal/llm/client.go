package llm

import (
	"context"
	"fmt"
	"os"

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
- Start with a Level 1 heading (# Title).
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
