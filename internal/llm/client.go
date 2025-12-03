package llm

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"text/template"

	"github.com/sashabaranov/go-openai"
)

//go:embed prompts/*.txt
var promptsFS embed.FS

type Client struct {
	client                           *openai.Client
	httpClient                       *http.Client
	baseUrl                          string
	ollamaBaseUrl                    string // Ollama native API URL (without /v1)
	thinkMode                        string // "true", "false", "low", "medium", "high"
	modelGenerateArticle             string
	modelExtractEntities             string
	modelSuggestTopics               string
	modelSummarizePlain              string
	modelSummarizeJSON               string
	generateArticleSystemTemplate    *template.Template
	generateArticleUserTemplate      *template.Template
	extractEntitiesSystemTemplate    *template.Template
	extractEntitiesUserTemplate      *template.Template
	suggestTopicsSystemTemplate      *template.Template
	suggestTopicsUserTemplate        *template.Template
	summarizeSourceSystemTemplate    *template.Template
	summarizeSourceUserTemplate      *template.Template
	convertSummarySystemTemplate     *template.Template
	convertSummaryUserTemplate       *template.Template
	addReferencesSystemTemplate      *template.Template
	addReferencesUserTemplate        *template.Template
}

// ThinkingCallback is called when thinking output is available
type ThinkingCallback func(taskName, model, thinking string)

func NewClient() (*Client, error) {
	apiKey := os.Getenv("LLM_API_KEY")
	if apiKey == "" {
		// Ollama does not require an API key, but the OpenAI client expects one.
		apiKey = "ollama"
	}

	baseUrl := os.Getenv("LLM_BASE_URL")
	if baseUrl == "" {
		// Backwards compatibility with previous OPENAI_BASE_URL env var
		baseUrl = os.Getenv("OPENAI_BASE_URL")
	}
	if baseUrl == "" {
		baseUrl = "http://localhost:11434/v1"
	}

	// Derive Ollama native API URL (strip /v1 suffix if present)
	ollamaBaseUrl := strings.TrimSuffix(baseUrl, "/v1")
	ollamaBaseUrl = strings.TrimSuffix(ollamaBaseUrl, "/")

	// Think mode configuration
	thinkMode := os.Getenv("LLM_THINK_MODE")
	if thinkMode == "" {
		thinkMode = "false"
	}
	if thinkMode != "false" {
		log.Printf("LLM Think Mode enabled: %s", thinkMode)
	}

	// Model configuration - multi-model support for optimized performance
	// If LLM_MODEL is set, use it for all tasks (backwards compatibility)
	// Otherwise, use task-specific models
	var modelGenerateArticle, modelExtractEntities, modelSuggestTopics, modelSummarizePlain, modelSummarizeJSON string
	
	legacyModel := os.Getenv("LLM_MODEL")
	if legacyModel != "" {
		// Backwards compatibility: use single model for all tasks
		modelGenerateArticle = legacyModel
		modelExtractEntities = legacyModel
		modelSuggestTopics = legacyModel
		modelSummarizePlain = legacyModel
		modelSummarizeJSON = legacyModel
		log.Printf("Using single model for all tasks (legacy mode): %s", legacyModel)
	} else {
		// Multi-model configuration
		// Fast tasks: topic suggestion, JSON conversion
		modelFast := os.Getenv("LLM_MODEL_FAST")
		if modelFast == "" {
			// Fallback to legacy variable names
			if legacy := os.Getenv("LLM_MODEL_DETAILED"); legacy != "" {
				modelFast = legacy
			} else if legacy := os.Getenv("OPENAI_MODEL"); legacy != "" {
				modelFast = legacy
			} else {
				modelFast = "qwen3:7b"
			}
		}
		
		// Entity extraction: medium model
		modelEntity := os.Getenv("LLM_MODEL_ENTITY")
		if modelEntity == "" {
			modelEntity = "qwen3:14b"
		}
		
		// Article generation: large model
		modelArticle := os.Getenv("LLM_MODEL_ARTICLE")
		if modelArticle == "" {
			modelArticle = "qwen3:32b"
		}
		
		// Assign models to tasks
		modelGenerateArticle = modelArticle
		modelExtractEntities = modelEntity
		modelSuggestTopics = modelFast
		modelSummarizePlain = modelEntity  // Use medium model if LLM summarization enabled
		modelSummarizeJSON = modelFast     // Use fast model for JSON conversion
		
		log.Printf("Multi-model configuration: Fast=%s, Entity=%s, Article=%s", modelFast, modelEntity, modelArticle)
	}

	config := openai.DefaultConfig(apiKey)
	config.BaseURL = baseUrl

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

	summarizeSourceSystem, err := loadTemplate("prompts/phase_1_step_1_summarize_source_system.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load summarize_source_system template: %w", err)
	}

	summarizeSourceUser, err := loadTemplate("prompts/phase_1_step_1_summarize_source_user.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load summarize_source_user template: %w", err)
	}

	convertSummarySystem, err := loadTemplate("prompts/phase_1_step_2_convert_summary_system.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load convert_summary_system template: %w", err)
	}

	convertSummaryUser, err := loadTemplate("prompts/phase_1_step_2_convert_summary_user.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load convert_summary_user template: %w", err)
	}

	addReferencesSystem, err := loadTemplate("prompts/add_references_system.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load add_references_system template: %w", err)
	}

	addReferencesUser, err := loadTemplate("prompts/add_references_user.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load add_references_user template: %w", err)
	}

	return &Client{
		client:                        openai.NewClientWithConfig(config),
		httpClient:                    &http.Client{},
		baseUrl:                       baseUrl,
		ollamaBaseUrl:                 ollamaBaseUrl,
		thinkMode:                     thinkMode,
		modelGenerateArticle:          modelGenerateArticle,
		modelExtractEntities:          modelExtractEntities,
		modelSuggestTopics:            modelSuggestTopics,
		modelSummarizePlain:           modelSummarizePlain,
		modelSummarizeJSON:            modelSummarizeJSON,
		generateArticleSystemTemplate: generateArticleSystem,
		generateArticleUserTemplate:   generateArticleUser,
		extractEntitiesSystemTemplate: extractEntitiesSystem,
		extractEntitiesUserTemplate:   extractEntitiesUser,
		suggestTopicsSystemTemplate:   suggestTopicsSystem,
		suggestTopicsUserTemplate:     suggestTopicsUser,
		summarizeSourceSystemTemplate: summarizeSourceSystem,
		summarizeSourceUserTemplate:   summarizeSourceUser,
		convertSummarySystemTemplate:  convertSummarySystem,
		convertSummaryUserTemplate:    convertSummaryUser,
		addReferencesSystemTemplate:   addReferencesSystem,
		addReferencesUserTemplate:     addReferencesUser,
	}, nil
}

// ollamaChatMessage represents a message in Ollama's chat format
type ollamaChatMessage struct {
	Role     string `json:"role"`
	Content  string `json:"content"`
	Thinking string `json:"thinking,omitempty"`
}

// ollamaChatRequest represents an Ollama chat API request
type ollamaChatRequest struct {
	Model    string              `json:"model"`
	Messages []ollamaChatMessage `json:"messages"`
	Stream   bool                `json:"stream"`
	Think    interface{}         `json:"think,omitempty"` // bool or string ("low", "medium", "high")
	Options  map[string]float64  `json:"options,omitempty"`
}

// ollamaChatResponse represents an Ollama chat API response
type ollamaChatResponse struct {
	Model   string `json:"model"`
	Message struct {
		Role     string `json:"role"`
		Content  string `json:"content"`
		Thinking string `json:"thinking,omitempty"`
	} `json:"message"`
	Done bool `json:"done"`
}

// chatWithThinking calls Ollama's native API with thinking enabled
func (c *Client) chatWithThinking(ctx context.Context, model string, messages []ollamaChatMessage, temperature float64) (*ollamaChatResponse, error) {
	// Determine think parameter value
	var thinkParam interface{}
	switch c.thinkMode {
	case "false", "":
		thinkParam = false
	case "true":
		thinkParam = true
	case "low", "medium", "high":
		thinkParam = c.thinkMode
	default:
		thinkParam = true
	}

	req := ollamaChatRequest{
		Model:    model,
		Messages: messages,
		Stream:   false,
		Think:    thinkParam,
		Options:  map[string]float64{"temperature": temperature},
	}

	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.ollamaBaseUrl+"/api/chat", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama API error (status %d): %s", resp.StatusCode, string(body))
	}

	var chatResp ollamaChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &chatResp, nil
}

// ThinkingEnabled returns true if thinking mode is enabled
func (c *Client) ThinkingEnabled() bool {
	return c.thinkMode != "" && c.thinkMode != "false"
}

// GetLastThinking returns the thinking trace from the last API call (if available)
// This is a placeholder - actual implementation stores thinking in response
func (c *Client) GetLastThinking() string {
	return "" // Thinking is returned per-call now
}

func getEnvOrDefault(key, defaultValue string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultValue
}

func loadTemplate(path string) (*template.Template, error) {
	content, err := promptsFS.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return template.New(path).Parse(string(content))
}

func (c *Client) GenerateArticle(ctx context.Context, topic, contextData string) (*ArticleResult, error) {
	// Execute system template
	var systemBuf bytes.Buffer
	if err := c.generateArticleSystemTemplate.Execute(&systemBuf, nil); err != nil {
		return nil, fmt.Errorf("failed to execute system template: %w", err)
	}

	// Execute user template
	data := map[string]interface{}{
		"Topic":       topic,
		"ContextData": contextData,
	}
	var userBuf bytes.Buffer
	if err := c.generateArticleUserTemplate.Execute(&userBuf, data); err != nil {
		return nil, fmt.Errorf("failed to execute user template: %w", err)
	}

	// Use thinking mode if enabled
	if c.ThinkingEnabled() {
		log.Printf("GenerateArticle: Using thinking mode (%s) with model %s", c.thinkMode, c.modelGenerateArticle)
		messages := []ollamaChatMessage{
			{Role: "system", Content: systemBuf.String()},
			{Role: "user", Content: userBuf.String()},
		}
		resp, err := c.chatWithThinking(ctx, c.modelGenerateArticle, messages, 0.7)
		if err != nil {
			return nil, err
		}
		if resp.Message.Thinking != "" {
			log.Printf("GenerateArticle: Received thinking trace (%d chars)", len(resp.Message.Thinking))
		}
		return &ArticleResult{
			Content:  resp.Message.Content,
			Model:    c.modelGenerateArticle,
			Thinking: resp.Message.Thinking,
		}, nil
	}

	// Standard OpenAI-compatible API call
	resp, err := c.client.CreateChatCompletion(
		ctx,
		openai.ChatCompletionRequest{
			Model: c.modelGenerateArticle,
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
		return nil, err
	}

	return &ArticleResult{
		Content: resp.Choices[0].Message.Content,
		Model:   c.modelGenerateArticle,
	}, nil
}

// AddReferences takes an article and source summaries, and adds inline citations
func (c *Client) AddReferences(ctx context.Context, article string, sources string) (string, error) {
	log.Printf("AddReferences: Adding citations using model %s", c.modelGenerateArticle)

	// Execute system template
	var systemBuf bytes.Buffer
	if err := c.addReferencesSystemTemplate.Execute(&systemBuf, nil); err != nil {
		return "", fmt.Errorf("failed to execute add_references system template: %w", err)
	}

	// Execute user template
	data := map[string]interface{}{
		"Article": article,
		"Sources": sources,
	}
	var userBuf bytes.Buffer
	if err := c.addReferencesUserTemplate.Execute(&userBuf, data); err != nil {
		return "", fmt.Errorf("failed to execute add_references user template: %w", err)
	}

	// Use thinking mode if enabled (helps with accurate citation placement)
	if c.ThinkingEnabled() {
		log.Printf("AddReferences: Using thinking mode (%s)", c.thinkMode)
		messages := []ollamaChatMessage{
			{Role: "system", Content: systemBuf.String()},
			{Role: "user", Content: userBuf.String()},
		}
		resp, err := c.chatWithThinking(ctx, c.modelGenerateArticle, messages, 0.3)
		if err != nil {
			return "", err
		}
		return resp.Message.Content, nil
	}

	// Standard API call
	resp, err := c.client.CreateChatCompletion(
		ctx,
		openai.ChatCompletionRequest{
			Model: c.modelGenerateArticle,
			Messages: []openai.ChatCompletionMessage{
				{Role: openai.ChatMessageRoleSystem, Content: systemBuf.String()},
				{Role: openai.ChatMessageRoleUser, Content: userBuf.String()},
			},
			Temperature: 0.3,
		},
	)
	if err != nil {
		return "", err
	}

	return resp.Choices[0].Message.Content, nil
}

func (c *Client) ExtractEntities(ctx context.Context, content string) ([]ExtractedEntity, error) {
	const maxRetries = 3

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

	var lastError error
	var lastRawResponse string

	for attempt := 1; attempt <= maxRetries; attempt++ {
		var rawContent string

		// Use thinking mode if enabled
		if c.ThinkingEnabled() {
			if attempt == 1 {
				log.Printf("ExtractEntities: Using thinking mode (%s) with model %s", c.thinkMode, c.modelExtractEntities)
			}
			messages := []ollamaChatMessage{
				{Role: "system", Content: systemBuf.String()},
				{Role: "user", Content: userBuf.String()},
			}

			// On retry attempts, add the failed response and a correction message
			if attempt > 1 && lastRawResponse != "" {
				messages = append(messages,
					ollamaChatMessage{Role: "assistant", Content: lastRawResponse},
					ollamaChatMessage{Role: "user", Content: "That response is invalid. You must respond with ONLY a JSON array, starting with [ and ending with ]. No markdown, no explanation, no headers. Just the JSON array. Try again:"},
				)
				log.Printf("Entity extraction retry %d/%d after non-JSON response", attempt, maxRetries)
			}

			resp, err := c.chatWithThinking(ctx, c.modelExtractEntities, messages, 0.0)
			if err != nil {
				lastError = err
				continue
			}
			rawContent = resp.Message.Content
		} else {
			// Standard OpenAI-compatible API call
			messages := []openai.ChatCompletionMessage{
				{Role: openai.ChatMessageRoleSystem, Content: systemBuf.String()},
				{Role: openai.ChatMessageRoleUser, Content: userBuf.String()},
			}

			// On retry attempts, add the failed response and a correction message
			if attempt > 1 && lastRawResponse != "" {
				messages = append(messages,
					openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant, Content: lastRawResponse},
					openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: "That response is invalid. You must respond with ONLY a JSON array, starting with [ and ending with ]. No markdown, no explanation, no headers. Just the JSON array. Try again:"},
				)
				log.Printf("Entity extraction retry %d/%d after non-JSON response", attempt, maxRetries)
			}

			resp, err := c.client.CreateChatCompletion(
				ctx,
				openai.ChatCompletionRequest{
					Model:       c.modelExtractEntities,
					Messages:    messages,
					Temperature: 0.0,
				},
			)
			if err != nil {
				lastError = err
				continue
			}
			rawContent = resp.Choices[0].Message.Content
		}

		jsonStr, found := extractJSONArray(rawContent)

		if !found {
			lastRawResponse = rawContent
			lastError = fmt.Errorf("non-JSON response: %.200s...", rawContent)
			continue
		}

		var entities []ExtractedEntity
		if err := json.Unmarshal([]byte(jsonStr), &entities); err != nil {
			lastRawResponse = rawContent
			lastError = fmt.Errorf("failed to parse entities JSON: %w (input: %q)", err, jsonStr)
			continue
		}

		// Success
		if attempt > 1 {
			log.Printf("Entity extraction succeeded on attempt %d", attempt)
		}
		return entities, nil
	}

	return nil, fmt.Errorf("entity extraction failed after %d attempts: %w", maxRetries, lastError)
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
			Model: c.modelSuggestTopics,
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

	rawContent := resp.Choices[0].Message.Content
	jsonStr, found := extractJSONArray(rawContent)
	
	if !found {
		log.Printf("Warning: Topic suggestion returned non-JSON response, returning empty topics. Raw response: %.200s...", rawContent)
		return []string{}, nil
	}

	var topics []string
	if err := json.Unmarshal([]byte(jsonStr), &topics); err != nil {
		return nil, fmt.Errorf("failed to parse topics JSON: %w (input: %q)", err, jsonStr)
	}

	return topics, nil
}

// SummarizeSource compresses a single source page into a focused summary and
// decides whether it is relevant enough to include.
//
// Uses a code-based filtering approach that preserves more content:
//  1) Code-based prefilter (removes web junk, keeps all content)
//  2) Code-based formatting (converts to bullet points)
//  3) Code-based topic extraction (from headings/structure)
//
// The LLM is NOT used for summarization to avoid content loss with smaller models.
func (c *Client) SummarizeSource(ctx context.Context, topic, urlStr, content string) (SourceSummary, error) {
	// Check if we should use the old LLM-based approach (for backwards compatibility)
	if os.Getenv("USE_LLM_SUMMARIZATION") == "true" {
		return c.summarizeSourceLLM(ctx, topic, urlStr, content)
	}

	// New code-based approach
	log.Printf("Phase 1 Step 1: Starting code-based prefilter for %s", urlStr)

	// Step 1: Code-based prefiltering
	filtered := PreFilterContent(content)
	filteredWordCount := len(strings.Fields(filtered))

	log.Printf("Phase 1 Step 1: Prefilter complete - %d chars → %d chars (%d words)",
		len(content), len(filtered), filteredWordCount)

	// Check if content is too short after filtering (likely junk page)
	if filteredWordCount < 50 {
		log.Printf("Phase 1 Step 1: Content too short after filtering (%d words), marking as NOT_RELEVANT", filteredWordCount)
		return SourceSummary{
			Model:       "code-prefilter",
			Language:    detectLanguage(content),
			Relevant:    false,
			Reason:      fmt.Sprintf("Content too short after filtering (%d words)", filteredWordCount),
			Summary:     "",
			Raw:         filtered,
			Step1Output: filtered,
		}, nil
	}

	// Step 2: Code-based formatting
	log.Printf("Phase 1 Step 2: Starting code-based formatting")
	formatted := FormatContent(filtered, topic)
	formattedWordCount := len(strings.Fields(formatted))

	log.Printf("Phase 1 Step 2: Formatting complete - %d words", formattedWordCount)

	// Step 3: Code-based topic extraction
	topics := ExtractTopicsFromContent(formatted)
	log.Printf("Phase 1 Step 3: Extracted %d topics from content structure", len(topics))

	// Build the summary result
	summary := SourceSummary{
		Model:       "code-prefilter",
		Language:    detectLanguage(content),
		Relevant:    true,
		Reason:      fmt.Sprintf("Content about %s (%d words)", topic, formattedWordCount),
		Summary:     formatted,
		Raw:         formatted,
		Step1Output: filtered, // Raw filtered content before formatting
	}

	return summary, nil
}

// summarizeSourceLLM is the old LLM-based approach, kept for backwards compatibility.
// Enable by setting USE_LLM_SUMMARIZATION=true
func (c *Client) summarizeSourceLLM(ctx context.Context, topic, urlStr, content string) (SourceSummary, error) {
	// Stage 1: plain-text summarization with headings and bullets
	var systemBuf bytes.Buffer
	if err := c.summarizeSourceSystemTemplate.Execute(&systemBuf, nil); err != nil {
		return SourceSummary{}, fmt.Errorf("failed to execute summarize system template: %w", err)
	}

	data := map[string]interface{}{
		"Topic":   topic,
		"URL":     urlStr,
		"Content": content,
	}
	var userBuf bytes.Buffer
	if err := c.summarizeSourceUserTemplate.Execute(&userBuf, data); err != nil {
		return SourceSummary{}, fmt.Errorf("failed to execute summarize user template: %w", err)
	}

	var plain string

	// Use thinking mode if enabled
	if c.ThinkingEnabled() {
		log.Printf("Stage 1: Starting LLM plain-text summarization with thinking (model: %s) for %s", c.modelSummarizePlain, urlStr)
		messages := []ollamaChatMessage{
			{Role: "system", Content: systemBuf.String()},
			{Role: "user", Content: userBuf.String()},
		}
		resp, err := c.chatWithThinking(ctx, c.modelSummarizePlain, messages, 0.3)
		if err != nil {
			return SourceSummary{}, err
		}
		plain = strings.TrimSpace(resp.Message.Content)
	} else {
		log.Printf("Stage 1: Starting LLM plain-text summarization (model: %s) for %s", c.modelSummarizePlain, urlStr)
		resp, err := c.client.CreateChatCompletion(
			ctx,
			openai.ChatCompletionRequest{
				Model: c.modelSummarizePlain,
				Messages: []openai.ChatCompletionMessage{
					{Role: openai.ChatMessageRoleSystem, Content: systemBuf.String()},
					{Role: openai.ChatMessageRoleUser, Content: userBuf.String()},
				},
				Temperature: 0.3,
			},
		)
		if err != nil {
			return SourceSummary{}, err
		}
		plain = strings.TrimSpace(resp.Choices[0].Message.Content)
	}

	log.Printf("Stage 1: Completed LLM plain-text summarization (model: %s), output length: %d chars", c.modelSummarizePlain, len(plain))

	summary := SourceSummary{
		Model:       c.modelSummarizePlain,
		Language:    detectLanguage(content),
		Raw:         plain,
		Step1Output: plain,
	}

	// If the model decides the page is not relevant, it should output NOT_RELEVANT.
	upper := strings.ToUpper(strings.TrimSpace(plain))
	if strings.HasPrefix(upper, "NOT_RELEVANT") {
		summary.Relevant = false
		summary.Reason = "Marked NOT_RELEVANT by summarization model"
		summary.Summary = ""
		log.Printf("Stage 1: Source marked as NOT_RELEVANT, skipping Stage 2")
		return summary, nil
	}

	// Stage 2: JSON conversion (relevance + reason + topics), using a fast model.
	log.Printf("Stage 2: Starting JSON conversion (model: %s) for %s", c.modelSummarizeJSON, urlStr)
	var convSystemBuf bytes.Buffer
	if err := c.convertSummarySystemTemplate.Execute(&convSystemBuf, nil); err != nil {
		summary.Relevant = true
		summary.Reason = "Failed to execute JSON conversion system prompt"
		summary.Summary = plain
		return summary, fmt.Errorf("failed to execute convert system template: %w", err)
	}

	convData := map[string]interface{}{
		"Content": plain,
	}
	var convUserBuf bytes.Buffer
	if err := c.convertSummaryUserTemplate.Execute(&convUserBuf, convData); err != nil {
		summary.Relevant = true
		summary.Reason = "Failed to execute JSON conversion user prompt"
		summary.Summary = plain
		return summary, fmt.Errorf("failed to execute convert user template: %w", err)
	}

	resp2, err := c.client.CreateChatCompletion(
		ctx,
		openai.ChatCompletionRequest{
			Model: c.modelSummarizeJSON,
			Messages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleSystem,
					Content: convSystemBuf.String(),
				},
				{
					Role:    openai.ChatMessageRoleUser,
					Content: convUserBuf.String(),
				},
			},
			Temperature: 0.0,
		},
	)
	if err != nil {
		summary.Relevant = true
		summary.Reason = fmt.Sprintf("JSON conversion request failed: %v", err)
		summary.Summary = plain
		return summary, err
	}

	rawJSON := strings.TrimSpace(resp2.Choices[0].Message.Content)
	summary.Raw = rawJSON

	jsonStr := extractJSONObject(rawJSON)
	var converted struct {
		Relevant bool     `json:"relevant"`
		Reason   string   `json:"reason"`
		Summary  string   `json:"summary"`
		Language string   `json:"language"`
		Topics   []string `json:"topics"`
	}

	if err := json.Unmarshal([]byte(jsonStr), &converted); err != nil {
		summary.Relevant = true
		summary.Reason = fmt.Sprintf("failed to parse JSON conversion: %v", err)
		summary.Summary = plain
		return summary, fmt.Errorf("failed to parse source summary JSON: %w (input: %q)", err, rawJSON)
	}

	summary.Relevant = converted.Relevant
	summary.Reason = converted.Reason
	if converted.Summary != "" {
		summary.Summary = converted.Summary
	} else {
		summary.Summary = plain
	}

	if converted.Language != "" {
		summary.Language = converted.Language
	}

	log.Printf("Stage 2: Completed JSON conversion (model: %s), relevant: %v, topics: %d", c.modelSummarizeJSON, summary.Relevant, len(converted.Topics))
	return summary, nil
}

// detectLanguage performs simple language detection based on common patterns.
// Returns ISO 639-1 language code (e.g., "en", "es", "fr", "de", "zh", "ja").
// Falls back to "en" if detection is uncertain.
func detectLanguage(text string) string {
	if len(text) < 100 {
		return "en" // Default for short text
	}

	// Sample first 2000 characters for analysis
	sample := text
	if len(sample) > 2000 {
		sample = sample[:2000]
	}
	sample = strings.ToLower(sample)

	// Common words/patterns for different languages
	langScores := map[string]int{
		"en": 0, // English (default)
		"es": 0, // Spanish
		"fr": 0, // French
		"de": 0, // German
		"it": 0, // Italian
		"pt": 0, // Portuguese
		"ru": 0, // Russian
		"zh": 0, // Chinese
		"ja": 0, // Japanese
		"ko": 0, // Korean
		"ar": 0, // Arabic
		"hi": 0, // Hindi
	}

	// English patterns
	englishWords := []string{" the ", " and ", " is ", " to ", " of ", " a ", " in ", " that ", " for ", " it "}
	for _, word := range englishWords {
		langScores["en"] += strings.Count(sample, word)
	}

	// Spanish patterns
	spanishWords := []string{" el ", " la ", " de ", " que ", " y ", " en ", " un ", " es ", " se ", " no "}
	for _, word := range spanishWords {
		langScores["es"] += strings.Count(sample, word)
	}

	// French patterns
	frenchWords := []string{" le ", " de ", " et ", " à ", " un ", " il ", " être ", " et ", " en ", " avoir "}
	for _, word := range frenchWords {
		langScores["fr"] += strings.Count(sample, word)
	}

	// German patterns
	germanWords := []string{" der ", " die ", " und ", " in ", " den ", " von ", " zu ", " das ", " mit ", " sich "}
	for _, word := range germanWords {
		langScores["de"] += strings.Count(sample, word)
	}

	// Chinese characters (CJK)
	if strings.ContainsAny(sample, "的一是在不了有和人这中大为上个国我以要他时来用们生到作地于出就分对成会可主发年动同工也能下过子说产种面而方后多定行学法所民得经十三之进着等部度家电力里如水化高自二理起小物现实加量都两体制机当使点从业本去把性好应开它合还因由其些然前外天政四日那社义事平形相全表间样与关各重新线内数正心反你明看原又么利比或但质气第向道命此变条只没结解问意建月公无系军很情者最立代想已通并提直题党程展五果料象员革位入常文总次品式活设及管特件长求老头基资边流路级少图山统接知较将组见计别她手角期根论运农指几九区强放决西被干做必战先回则任取据处队南给色光门即保治北造百规热领七海口东导器压志世金增争济阶油思术极交受联什认六共权收证改清己美再采转更单风切打白教速花带安场身车例真务具万每目至达走积示议声报斗完类八离华名确才科张信马节话米整空元况今集温传土许步群广石记需段研界拉林律叫且究观越织装影算低持音众书布复容儿须际商非验连断深难近矿千周委素技备半办青省列习响约支般史感劳便团转离习") {
		langScores["zh"] += 10 // Strong indicator
	}

	// Japanese characters (Hiragana/Katakana/Kanji)
	if strings.ContainsAny(sample, "あいうえおかきくけこさしすせそたちつてとなにぬねのはひふへほまみむめもやゆよらりるれろわをんアイウエオカキクケコサシスセソタチツテトナニヌネノハヒフヘホマミムメモヤユヨラリルレロワヲン") {
		langScores["ja"] += 10
	}

	// Korean characters
	if strings.ContainsAny(sample, "가나다라마바사아자차카타파하거너더러머버서어저처커터퍼허고노도로모보소오조초코토포호구누두루무부수우주추쿠투푸후그느드르므브스으즈츠크트프흐기니디리미비시이지치키티피히") {
		langScores["ko"] += 10
	}

	// Find language with highest score
	maxScore := 0
	detectedLang := "en" // Default
	for lang, score := range langScores {
		if score > maxScore {
			maxScore = score
			detectedLang = lang
		}
	}

	// If no strong signal, default to English
	if maxScore < 3 {
		return "en"
	}

	return detectedLang
}

// extractJSONArray finds the first '[' and last ']' to extract the JSON array.
// Returns the extracted JSON array, or "[]" if no valid array brackets are found.
// The second return value indicates whether an array was found.
func extractJSONArray(s string) (string, bool) {
	start := strings.Index(s, "[")
	end := strings.LastIndex(s, "]")
	if start == -1 || end == -1 || start > end {
		return "[]", false // Return empty array if pattern not found
	}
	return s[start : end+1], true
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

func (c *Client) CategorizeArticle(ctx context.Context, title string, tags []string, content string, existingCategories []string) (*ArticleCategory, error) {
	systemPrompt := `You are an article categorization expert for an encyclopedia. Your task is to determine the best category path for an article based on its title, tags, and content.

You must respond with a JSON object in this exact format:
{
  "category": "TopLevelCategory/Subcategory",
  "subcategory": "",
  "reasoning": "Brief explanation of why this category was chosen"
}

Category guidelines:
- Technology topics → Technology/<subtopic> (e.g., Technology/AI, Technology/Programming)
- Science topics → Science/<field> (e.g., Science/Physics, Science/Biology, Science/Chemistry)
- History topics → History/<era-or-region> (e.g., History/Ancient, History/Modern)
- Arts topics → Arts/<medium> (e.g., Arts/Music, Arts/Literature, Arts/Film)
- Geography topics → Geography/<region> (e.g., Geography/Europe, Geography/Asia)
- People/Biography → People/<field> (e.g., People/Scientists, People/Politicians)

Use existing categories when appropriate. Create new subcategories only when necessary.
The category path should be 2-3 levels deep maximum.`

	userPrompt := fmt.Sprintf(`Categorize this article:

Title: %s
Tags: %s

Content (first 2000 chars):
%s

Existing categories in the repository:
%s

Respond with JSON only.`, title, strings.Join(tags, ", "), truncateString(content, 2000), strings.Join(existingCategories, "\n"))

	resp, err := c.client.CreateChatCompletion(
		ctx,
		openai.ChatCompletionRequest{
			Model: c.modelExtractEntities, // Use same model as entity extraction
			Messages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleSystem,
					Content: systemPrompt,
				},
				{
					Role:    openai.ChatMessageRoleUser,
					Content: userPrompt,
				},
			},
			Temperature: 0.0, // Deterministic
		},
	)

	if err != nil {
		return nil, err
	}

	jsonStr := extractJSONObject(resp.Choices[0].Message.Content)

	var category ArticleCategory
	if err := json.Unmarshal([]byte(jsonStr), &category); err != nil {
		return nil, fmt.Errorf("failed to parse category JSON: %w (input: %q)", err, jsonStr)
	}

	return &category, nil
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
