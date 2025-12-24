package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"text/template"
	"time"

	"embed"

	"github.com/sashabaranov/go-openai"
)

//go:embed prompts/*.txt
var promptsFS embed.FS

type Client struct {
	client                            *openai.Client
	httpClient                        *http.Client
	modelGenerateArticle              string
	modelExtractEntities              string
	modelSuggestTopics                string
	modelSummarizePlain               string
	modelSummarizeJSON                string
	thinkMode                         string // "false", "true", "low", "medium", "high"
	ollamaBaseUrl                     string
	generateArticleSystemTemplate     *template.Template
	generateArticleUserTemplate       *template.Template
	extractEntitiesSystemTemplate     *template.Template
	extractEntitiesUserTemplate       *template.Template
	suggestTopicsSystemTemplate       *template.Template
	suggestTopicsUserTemplate         *template.Template
	summarizeSourceSystemTemplate     *template.Template
	summarizeSourceUserTemplate       *template.Template
	convertSummarySystemTemplate      *template.Template
	convertSummaryUserTemplate        *template.Template
	addReferencesSystemTemplate       *template.Template
	addReferencesUserTemplate         *template.Template
	generateMiniArticleSystemTemplate *template.Template
	generateMiniArticleUserTemplate   *template.Template
	checkRelevanceSystemTemplate      *template.Template
	checkRelevanceUserTemplate        *template.Template
	checkRedundancySystemTemplate     *template.Template
	checkRedundancyUserTemplate       *template.Template
	integrateContentSystemTemplate    *template.Template
	integrateContentUserTemplate      *template.Template
}

type ollamaChatRequest struct {
	Model    string                 `json:"model"`
	Messages []ollamaChatMessage    `json:"messages"`
	Stream   bool                   `json:"stream"`
	Think    interface{}            `json:"think,omitempty"` // boolean or string ("low", "medium", "high")
	Options  map[string]interface{} `json:"options,omitempty"`
}

type ollamaChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaChatResponse struct {
	Model   string `json:"model"`
	Message struct {
		Role     string `json:"role"`
		Content  string `json:"content"`
		Thinking string `json:"thinking,omitempty"`
	} `json:"message"`
}

// NewClient creates a new LLM client with default configuration
func NewClient() (*Client, error) {
	apiKey := os.Getenv("LLM_API_KEY")
	baseUrl := os.Getenv("LLM_BASE_URL")

	if apiKey == "" {
		apiKey = "ollama" // Default for Ollama
	}
	if baseUrl == "" {
		baseUrl = "http://localhost:11434/v1"
	}

	// Configure HTTP client with 15 minute timeout for large models
	httpClient := &http.Client{
		Timeout: 15 * time.Minute,
	}

	// Ollama base URL for native API (without /v1)
	ollamaBaseUrl := strings.TrimSuffix(baseUrl, "/v1")

	// Determine thinking mode
	thinkMode := os.Getenv("LLM_THINK_MODE")

	// Model selection
	var modelGenerateArticle, modelExtractEntities, modelSuggestTopics, modelSummarizePlain, modelSummarizeJSON string

	// Legacy fallback
	if legacyModel := os.Getenv("LLM_MODEL"); legacyModel != "" {
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

		// Article generation and entity extraction: large model for reliability
		// Entity extraction uses the same large model because smaller models struggle
		// to consistently output valid JSON with large inputs and thinking mode enabled
		modelArticle := os.Getenv("LLM_MODEL_ARTICLE")
		if modelArticle == "" {
			modelArticle = "qwen3:32b"
		}

		// Assign models to tasks
		modelGenerateArticle = modelArticle
		modelExtractEntities = modelArticle // Use large model for reliable JSON output
		modelSuggestTopics = modelFast
		modelSummarizePlain = modelArticle // Use large model for comprehensive summarization
		modelSummarizeJSON = modelFast     // Use fast model for JSON conversion

		log.Printf("Multi-model configuration: Fast=%s, Article/Entity/Summarize=%s", modelFast, modelArticle)
	}

	config := openai.DefaultConfig(apiKey)
	config.BaseURL = baseUrl
	config.HTTPClient = httpClient // Set custom HTTP client with timeout

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

	generateMiniArticleSystem, err := loadTemplate("prompts/generate_mini_article_system.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load generate_mini_article_system template: %w", err)
	}

	generateMiniArticleUser, err := loadTemplate("prompts/generate_mini_article_user.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load generate_mini_article_user template: %w", err)
	}

	checkRelevanceSystem, err := loadTemplate("prompts/check_relevance_system.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load check_relevance_system template: %w", err)
	}

	checkRelevanceUser, err := loadTemplate("prompts/check_relevance_user.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load check_relevance_user template: %w", err)
	}

	checkRedundancySystem, err := loadTemplate("prompts/check_redundancy_system.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load check_redundancy_system template: %w", err)
	}

	checkRedundancyUser, err := loadTemplate("prompts/check_redundancy_user.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load check_redundancy_user template: %w", err)
	}

	integrateContentSystem, err := loadTemplate("prompts/integrate_content_system.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load integrate_content_system template: %w", err)
	}

	integrateContentUser, err := loadTemplate("prompts/integrate_content_user.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load integrate_content_user template: %w", err)
	}

	return &Client{
		client:                            openai.NewClientWithConfig(config),
		httpClient:                        httpClient,
		modelGenerateArticle:              modelGenerateArticle,
		modelExtractEntities:              modelExtractEntities,
		modelSuggestTopics:                modelSuggestTopics,
		modelSummarizePlain:               modelSummarizePlain,
		modelSummarizeJSON:                modelSummarizeJSON,
		thinkMode:                         thinkMode,
		ollamaBaseUrl:                     ollamaBaseUrl,
		generateArticleSystemTemplate:     generateArticleSystem,
		generateArticleUserTemplate:       generateArticleUser,
		extractEntitiesSystemTemplate:     extractEntitiesSystem,
		extractEntitiesUserTemplate:       extractEntitiesUser,
		suggestTopicsSystemTemplate:       suggestTopicsSystem,
		suggestTopicsUserTemplate:         suggestTopicsUser,
		summarizeSourceSystemTemplate:     summarizeSourceSystem,
		summarizeSourceUserTemplate:       summarizeSourceUser,
		convertSummarySystemTemplate:      convertSummarySystem,
		convertSummaryUserTemplate:        convertSummaryUser,
		addReferencesSystemTemplate:       addReferencesSystem,
		addReferencesUserTemplate:         addReferencesUser,
		generateMiniArticleSystemTemplate: generateMiniArticleSystem,
		generateMiniArticleUserTemplate:   generateMiniArticleUser,
		checkRelevanceSystemTemplate:      checkRelevanceSystem,
		checkRelevanceUserTemplate:        checkRelevanceUser,
		checkRedundancySystemTemplate:     checkRedundancySystem,
		checkRedundancyUserTemplate:       checkRedundancyUser,
		integrateContentSystemTemplate:    integrateContentSystem,
		integrateContentUserTemplate:      integrateContentUser,
	}, nil
}

// chatWithThinking calls Ollama's native API with thinking enabled
// numPredict controls max output tokens (0 = model default)
func (c *Client) chatWithThinking(ctx context.Context, model string, messages []ollamaChatMessage, temperature float64, numPredict ...int) (*ollamaChatResponse, error) {
	return c.chatOllama(ctx, model, messages, temperature, true, numPredict...)
}

// chatNoThinking calls Ollama's native API with thinking DISABLED
// Use this for tasks where longer output is needed (thinking consumes output tokens)
func (c *Client) chatNoThinking(ctx context.Context, model string, messages []ollamaChatMessage, temperature float64, numPredict ...int) (*ollamaChatResponse, error) {
	return c.chatOllama(ctx, model, messages, temperature, false, numPredict...)
}

// chatOllama is the core Ollama API call
func (c *Client) chatOllama(ctx context.Context, model string, messages []ollamaChatMessage, temperature float64, useThinking bool, numPredict ...int) (*ollamaChatResponse, error) {
	// Determine think parameter value
	var thinkParam interface{} = false
	if useThinking {
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
	}

	options := map[string]interface{}{"temperature": temperature}
	if len(numPredict) > 0 && numPredict[0] > 0 {
		options["num_predict"] = numPredict[0]
	}

	req := ollamaChatRequest{
		Model:    model,
		Messages: messages,
		Stream:   false,
		Think:    thinkParam,
		Options:  options,
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
		"Topic":   topic,
		"Context": contextData,
	}
	var userBuf bytes.Buffer
	if err := c.generateArticleUserTemplate.Execute(&userBuf, data); err != nil {
		return nil, fmt.Errorf("failed to execute user template: %w", err)
	}

	// Use thinking mode if enabled
	if c.ThinkingEnabled() {
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
	startTime := time.Now()
	inputLen := len(article)
	log.Printf("AddReferences: Starting citation addition (article: %d chars, model: %s, thinking: %v)",
		inputLen, c.modelGenerateArticle, c.ThinkingEnabled())

	const maxRetries = 3
	var lastError error

	for attempt := 1; attempt <= maxRetries; attempt++ {
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

		// Add retry instruction if this is a retry
		if attempt > 1 {
			userBuf.WriteString("\n\nCRITICAL: Your previous response was too short and lost content. You MUST preserve ALL article content. The output must be at least as long as the input. Only add citation markers [^1], [^2], etc. - do NOT remove or summarize any content.")
		}

		// Use thinking mode if enabled (helps with accurate citation placement)
		var result string
		if c.ThinkingEnabled() {
			messages := []ollamaChatMessage{
				{Role: "system", Content: systemBuf.String()},
				{Role: "user", Content: userBuf.String()},
			}
			resp, err := c.chatWithThinking(ctx, c.modelGenerateArticle, messages, 0.3)
			if err != nil {
				lastError = err
				log.Printf("AddReferences: Attempt %d/%d failed after %v - %v", attempt, maxRetries, time.Since(startTime), err)
				if attempt < maxRetries {
					continue
				}
				return "", fmt.Errorf("add references failed after %d attempts: %w", maxRetries, err)
			}
			result = resp.Message.Content
		} else {
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
				lastError = err
				log.Printf("AddReferences: Attempt %d/%d failed after %v - %v", attempt, maxRetries, time.Since(startTime), err)
				if attempt < maxRetries {
					continue
				}
				return "", fmt.Errorf("add references failed after %d attempts: %w", maxRetries, err)
			}
			result = resp.Choices[0].Message.Content
		}

		// Validation: Output must not be shorter than input
		outputLen := len(result)
		if outputLen < inputLen {
			log.Printf("AddReferences: Attempt %d/%d - Output is shorter than input (%d < %d chars). Retrying...",
				attempt, maxRetries, outputLen, inputLen)
			lastError = fmt.Errorf("output too short: %d < %d chars", outputLen, inputLen)
			if attempt < maxRetries {
				continue
			}
			// Final attempt failed - return original article rather than truncated output
			log.Printf("AddReferences: All attempts failed to preserve content length. Returning original article without citations.")
			return article, nil
		}

		// Success
		if attempt > 1 {
			log.Printf("AddReferences: Succeeded on attempt %d", attempt)
		}
		log.Printf("AddReferences: Completed in %v (input: %d chars, output: %d chars)", time.Since(startTime), inputLen, outputLen)
		return result, nil
	}

	// Should not reach here, but handle it
	if lastError != nil {
		return "", fmt.Errorf("add references failed after %d attempts: %w", maxRetries, lastError)
	}
	log.Printf("AddReferences: All attempts failed. Returning original article without citations.")
	return article, nil
}

// ExtractEntities extracts named entities from content.
// For large inputs (>8K chars), it chunks the content and merges results.
// Thinking mode is DISABLED for entity extraction to prevent multi-hour hangs.
func (c *Client) ExtractEntities(ctx context.Context, content string) ([]ExtractedEntity, error) {
	startTime := time.Now()
	const chunkSize = 8000 // 8K chars per chunk
	const maxRetries = 3

	// For large content, chunk it and process each chunk
	if len(content) > chunkSize {
		log.Printf("ExtractEntities: Large input (%d chars), splitting into chunks of %d", len(content), chunkSize)
		return c.extractEntitiesChunked(ctx, content, chunkSize)
	}

	log.Printf("ExtractEntities: Starting (model: %s, input: %d chars)", c.modelExtractEntities, len(content))

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
		// Create a context with timeout to prevent multi-hour hangs
		attemptCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()

		var rawContent string

		// Standard OpenAI-compatible API call (NO thinking mode for entity extraction)
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
			attemptCtx,
			openai.ChatCompletionRequest{
				Model:       c.modelExtractEntities,
				Messages:    messages,
				Temperature: 0.0,
			},
		)
		if err != nil {
			if attemptCtx.Err() == context.DeadlineExceeded {
				log.Printf("ExtractEntities: Attempt %d timed out after 10 minutes", attempt)
			}
			lastError = err
			continue
		}
		rawContent = resp.Choices[0].Message.Content

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
			log.Printf("ExtractEntities: Succeeded on attempt %d", attempt)
		}
		log.Printf("ExtractEntities: Completed in %v, found %d entities", time.Since(startTime), len(entities))
		return entities, nil
	}

	log.Printf("ExtractEntities: FAILED after %d attempts and %v - %v", maxRetries, time.Since(startTime), lastError)
	return nil, fmt.Errorf("entity extraction failed after %d attempts: %w", maxRetries, lastError)
}

// extractEntitiesChunked processes large content by splitting into chunks
func (c *Client) extractEntitiesChunked(ctx context.Context, content string, chunkSize int) ([]ExtractedEntity, error) {
	startTime := time.Now()

	// Split content into chunks at sentence boundaries
	chunks := splitIntoChunks(content, chunkSize)
	log.Printf("ExtractEntities: Split into %d chunks", len(chunks))

	var allEntities []ExtractedEntity
	seenEntities := make(map[string]bool) // Dedupe by name+type

	for i, chunk := range chunks {
		log.Printf("ExtractEntities: Processing chunk %d/%d (%d chars)", i+1, len(chunks), len(chunk))

		entities, err := c.extractEntitiesSingle(ctx, chunk)
		if err != nil {
			log.Printf("ExtractEntities: Chunk %d failed: %v (continuing)", i+1, err)
			continue // Don't fail entirely if one chunk fails
		}

		// Dedupe and merge
		for _, e := range entities {
			key := string(e.Type) + ":" + strings.ToLower(e.Name)
			if !seenEntities[key] {
				seenEntities[key] = true
				allEntities = append(allEntities, e)
			}
		}
		log.Printf("ExtractEntities: Chunk %d found %d entities (total unique: %d)", i+1, len(entities), len(allEntities))
	}

	log.Printf("ExtractEntities: Chunked extraction completed in %v, found %d unique entities", time.Since(startTime), len(allEntities))
	return allEntities, nil
}

// extractEntitiesSingle extracts entities from a single chunk (no further splitting)
func (c *Client) extractEntitiesSingle(ctx context.Context, content string) ([]ExtractedEntity, error) {
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
		// Create a context with timeout
		attemptCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)

		messages := []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: systemBuf.String()},
			{Role: openai.ChatMessageRoleUser, Content: userBuf.String()},
		}

		if attempt > 1 && lastRawResponse != "" {
			messages = append(messages,
				openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant, Content: lastRawResponse},
				openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: "That response is invalid. You must respond with ONLY a JSON array, starting with [ and ending with ]. No markdown, no explanation, no headers. Just the JSON array. Try again:"},
			)
		}

		resp, err := c.client.CreateChatCompletion(
			attemptCtx,
			openai.ChatCompletionRequest{
				Model:       c.modelExtractEntities,
				Messages:    messages,
				Temperature: 0.0,
			},
		)
		cancel() // Clean up timeout context

		if err != nil {
			lastError = err
			continue
		}

		jsonStr, found := extractJSONArray(resp.Choices[0].Message.Content)
		if !found {
			lastRawResponse = resp.Choices[0].Message.Content
			lastError = fmt.Errorf("non-JSON response")
			continue
		}

		var entities []ExtractedEntity
		if err := json.Unmarshal([]byte(jsonStr), &entities); err != nil {
			lastRawResponse = resp.Choices[0].Message.Content
			lastError = fmt.Errorf("failed to parse JSON: %w", err)
			continue
		}

		return entities, nil
	}

	return nil, lastError
}

// splitIntoChunks splits content into chunks at paragraph/sentence boundaries
func splitIntoChunks(content string, maxSize int) []string {
	if len(content) <= maxSize {
		return []string{content}
	}

	var chunks []string
	remaining := content

	for len(remaining) > 0 {
		if len(remaining) <= maxSize {
			chunks = append(chunks, remaining)
			break
		}

		// Find a good break point (paragraph or sentence)
		chunk := remaining[:maxSize]
		breakPoint := maxSize

		// Try to break at paragraph
		if idx := strings.LastIndex(chunk, "\n\n"); idx > maxSize/2 {
			breakPoint = idx + 2
		} else if idx := strings.LastIndex(chunk, "\n"); idx > maxSize/2 {
			// Break at newline
			breakPoint = idx + 1
		} else if idx := strings.LastIndex(chunk, ". "); idx > maxSize/2 {
			// Break at sentence
			breakPoint = idx + 2
		}

		chunks = append(chunks, remaining[:breakPoint])
		remaining = remaining[breakPoint:]
	}

	return chunks
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

func extractJSONArray(s string) (string, bool) {
	// Find first [ and last ]
	start := strings.Index(s, "[")
	end := strings.LastIndex(s, "]")
	if start >= 0 && end > start {
		return s[start : end+1], true
	}
	return "", false
}

func (c *Client) SummarizeSource(ctx context.Context, topic, urlStr, content string) (SourceSummary, error) {
	// Log input content length for debugging
	log.Printf("SummarizeSource: Received %d chars of input content for %s", len(content), urlStr)

	// Step 1: Summarize content to plain text using chunked approach for large documents
	var plain string
	const chunkSize = 15000 // ~15k chars per chunk for manageable LLM processing

	if len(content) > chunkSize {
		// Use chunked summarization for large documents
		log.Printf("Stage 1: Large document (%d chars), using chunked summarization", len(content))
		chunkedResult, err := c.summarizeSourceChunked(ctx, topic, urlStr, content, chunkSize)
		if err != nil {
			return SourceSummary{}, err
		}
		plain = chunkedResult
	} else {
		// Single-pass summarization for smaller documents
		var systemBuf bytes.Buffer
		if err := c.summarizeSourceSystemTemplate.Execute(&systemBuf, nil); err != nil {
			return SourceSummary{}, fmt.Errorf("failed to execute summarize_source system template: %w", err)
		}

		data := map[string]interface{}{
			"Topic":   topic,
			"URL":     urlStr,
			"Content": content,
		}
		var userBuf bytes.Buffer
		if err := c.summarizeSourceUserTemplate.Execute(&userBuf, data); err != nil {
			return SourceSummary{}, fmt.Errorf("failed to execute summarize_source user template: %w", err)
		}

		log.Printf("Stage 1: Starting LLM plain-text summarization (model: %s, thinking DISABLED for longer output) for %s", c.modelSummarizePlain, urlStr)
		messages := []ollamaChatMessage{
			{Role: "system", Content: systemBuf.String()},
			{Role: "user", Content: userBuf.String()},
		}
		// Request 16000 tokens for comprehensive extraction, with thinking DISABLED
		resp, err := c.chatNoThinking(ctx, c.modelSummarizePlain, messages, 0.3, 16000)
		if err != nil {
			return SourceSummary{}, err
		}
		plain = strings.TrimSpace(resp.Message.Content)
	}

	log.Printf("Stage 1: Completed LLM plain-text summarization (model: %s), output length: %d chars", c.modelSummarizePlain, len(plain))

	summary := SourceSummary{
		Model:       c.modelSummarizePlain,
		Language:    detectLanguage(content),
		Raw:         plain,
		Step1Output: plain,
	}

	// Step 2: Convert plain text summary to structured JSON
	log.Printf("Stage 2: Converting summary to JSON (model: %s)", c.modelSummarizeJSON)

	var sysBuf2 bytes.Buffer
	if err := c.convertSummarySystemTemplate.Execute(&sysBuf2, nil); err != nil {
		return summary, fmt.Errorf("failed to execute convert_summary system template: %w", err)
	}

	var userBuf2 bytes.Buffer
	data2 := map[string]interface{}{
		"Topic":   topic,
		"URL":     urlStr,
		"Summary": plain,
	}
	if err := c.convertSummaryUserTemplate.Execute(&userBuf2, data2); err != nil {
		return summary, fmt.Errorf("failed to execute convert_summary user template: %w", err)
	}

	resp2, err := c.client.CreateChatCompletion(
		ctx,
		openai.ChatCompletionRequest{
			Model: c.modelSummarizeJSON,
			Messages: []openai.ChatCompletionMessage{
				{Role: openai.ChatMessageRoleSystem, Content: sysBuf2.String()},
				{Role: openai.ChatMessageRoleUser, Content: userBuf2.String()},
			},
			Temperature: 0.0, // JSON conversion should be deterministic
		},
	)
	if err != nil {
		return summary, err
	}

	rawJSON := strings.TrimSpace(resp2.Choices[0].Message.Content)
	summary.Raw = rawJSON

	jsonStr := extractJSONObject(rawJSON)
	var converted struct {
		Relevant bool   `json:"relevant"`
		Reason   string `json:"reason,omitempty"`
		Summary  string `json:"summary"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &converted); err != nil {
		log.Printf("Warning: Failed to parse step 2 JSON: %v. Falling back to plain text. Raw JSON: %.200s...", err, rawJSON)
		// Fallback: treat plain text as summary and assume relevant
		summary.Summary = plain
		summary.Relevant = true
		summary.Reason = "Fallback from plain text summarization"
	} else {
		// Check if summary is empty even after successful JSON parse
		if strings.TrimSpace(converted.Summary) == "" {
			log.Printf("Warning: JSON conversion produced empty summary (relevant=%v, reason=%q). Falling back to plain text (plain length: %d chars)",
				converted.Relevant, converted.Reason, len(plain))
			summary.Summary = plain
			summary.Relevant = true
			summary.Reason = "Fallback: JSON conversion produced empty summary"
		} else {
			summary.Relevant = converted.Relevant
			summary.Reason = converted.Reason
			summary.Summary = converted.Summary
			log.Printf("Stage 2: Successfully converted to JSON (relevant=%v, summary length=%d chars)",
				summary.Relevant, len(summary.Summary))
		}
	}

	return summary, nil
}

// summarizeSourceChunked splits large content into chunks and summarizes each
func (c *Client) summarizeSourceChunked(ctx context.Context, topic, urlStr, content string, chunkSize int) (string, error) {
	chunks := splitIntoChunks(content, chunkSize)
	log.Printf("Stage 1: Split into %d chunks for summarization", len(chunks))

	var allSummaries strings.Builder
	
	for i, chunk := range chunks {
		log.Printf("Stage 1: Processing chunk %d/%d (%d chars)", i+1, len(chunks), len(chunk))

		var systemBuf bytes.Buffer
		if err := c.summarizeSourceSystemTemplate.Execute(&systemBuf, nil); err != nil {
			return "", fmt.Errorf("failed to execute summarize_source system template: %w", err)
		}

		data := map[string]interface{}{
			"Topic":   topic,
			"URL":     urlStr,
			"Content": chunk,
		}
		var userBuf bytes.Buffer
		if err := c.summarizeSourceUserTemplate.Execute(&userBuf, data); err != nil {
			return "", fmt.Errorf("failed to execute summarize_source user template: %w", err)
		}

		messages := []ollamaChatMessage{
			{Role: "system", Content: systemBuf.String()},
			{Role: "user", Content: userBuf.String()},
		}

		resp, err := c.chatNoThinking(ctx, c.modelSummarizePlain, messages, 0.3, 8000)
		if err != nil {
			log.Printf("Stage 1: Chunk %d failed: %v (continuing)", i+1, err)
			continue
		}

		chunkSummary := strings.TrimSpace(resp.Message.Content)
		log.Printf("Stage 1: Chunk %d produced %d chars", i+1, len(chunkSummary))

		// Skip if chunk returned "NOT_RELEVANT" or similar
		if strings.HasPrefix(strings.ToUpper(chunkSummary), "NOT_RELEVANT") {
			log.Printf("Stage 1: Chunk %d marked as not relevant, skipping", i+1)
			continue
		}

		if allSummaries.Len() > 0 {
			allSummaries.WriteString("\n\n")
		}
		allSummaries.WriteString(chunkSummary)
	}

	result := allSummaries.String()
	log.Printf("Stage 1: Combined all chunks into %d chars total", len(result))
	return result, nil
}

func extractJSONObject(s string) string {
	// Find first { and last }
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start >= 0 && end > start {
		return s[start : end+1]
	}
	return s
}

func detectLanguage(content string) string {
	// Simple heuristic or placeholder
	return "en"
}

func (c *Client) CategorizeArticle(ctx context.Context, title string, tags []string, content string, existingCategories []string) (*ArticleCategory, error) {
	// Simplified implementation - assume category from existing or prompt
	// For now, this method might not be used in the current refactor, but keeping interface satisfaction
	return &ArticleCategory{
		Category:    "General",
		Subcategory: "General",
		Reasoning:   "Default categorization",
	}, nil
}

func (c *Client) GenerateMiniArticle(ctx context.Context, topic, sourceTitle, sourceSummary string) (string, error) {
	startTime := time.Now()

	// Execute system template
	var systemBuf bytes.Buffer
	if err := c.generateMiniArticleSystemTemplate.Execute(&systemBuf, nil); err != nil {
		return "", fmt.Errorf("failed to execute generate_mini_article system template: %w", err)
	}

	// Execute user template
	data := map[string]interface{}{
		"Topic":         topic,
		"SourceTitle":   sourceTitle,
		"SourceSummary": sourceSummary,
	}
	var userBuf bytes.Buffer
	if err := c.generateMiniArticleUserTemplate.Execute(&userBuf, data); err != nil {
		return "", fmt.Errorf("failed to execute generate_mini_article user template: %w", err)
	}

	// Use thinking mode if enabled
	if c.ThinkingEnabled() {
		messages := []ollamaChatMessage{
			{Role: "system", Content: systemBuf.String()},
			{Role: "user", Content: userBuf.String()},
		}
		resp, err := c.chatWithThinking(ctx, c.modelGenerateArticle, messages, 0.7)
		if err != nil {
			return "", err
		}
		log.Printf("GenerateMiniArticle: Completed in %v (%d chars)", time.Since(startTime), len(resp.Message.Content))
		return resp.Message.Content, nil
	}

	resp, err := c.client.CreateChatCompletion(
		ctx,
		openai.ChatCompletionRequest{
			Model: c.modelGenerateArticle,
			Messages: []openai.ChatCompletionMessage{
				{Role: openai.ChatMessageRoleSystem, Content: systemBuf.String()},
				{Role: openai.ChatMessageRoleUser, Content: userBuf.String()},
			},
			Temperature: 0.7,
		},
	)
	if err != nil {
		return "", err
	}

	result := resp.Choices[0].Message.Content
	log.Printf("GenerateMiniArticle: Completed in %v (%d chars)", time.Since(startTime), len(result))
	return result, nil
}

func (c *Client) CheckRelevance(ctx context.Context, topic, content string) (*RelevanceResult, error) {
	startTime := time.Now()

	// Execute system template
	var systemBuf bytes.Buffer
	if err := c.checkRelevanceSystemTemplate.Execute(&systemBuf, nil); err != nil {
		return nil, fmt.Errorf("failed to execute check_relevance system template: %w", err)
	}

	// Execute user template
	data := map[string]interface{}{
		"Topic":   topic,
		"Content": content,
	}
	var userBuf bytes.Buffer
	if err := c.checkRelevanceUserTemplate.Execute(&userBuf, data); err != nil {
		return nil, fmt.Errorf("failed to execute check_relevance user template: %w", err)
	}

	// Use fast model for checks, low temp
	model := c.modelSuggestTopics // Using fast model
	if c.ThinkingEnabled() {
		// Even with thinking enabled, validation checks are better on low temp
		messages := []ollamaChatMessage{
			{Role: "system", Content: systemBuf.String()},
			{Role: "user", Content: userBuf.String()},
		}
		resp, err := c.chatWithThinking(ctx, model, messages, 0.1)
		if err != nil {
			return nil, err
		}

		jsonStr := extractJSONObject(resp.Message.Content)
		if !strings.Contains(jsonStr, "{") {
			// Try to extract JSON from thinking trace if available
			if resp.Message.Thinking != "" {
				jsonStr = extractJSONObject(resp.Message.Thinking)
			}
			if !strings.Contains(jsonStr, "{") {
				log.Printf("CheckRelevance: Raw response (first 500 chars): %.500s", resp.Message.Content)
				return nil, fmt.Errorf("non-JSON response from check_relevance")
			}
		}

		var result RelevanceResult
		if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
			return nil, fmt.Errorf("failed to parse check_relevance JSON: %w", err)
		}
		log.Printf("CheckRelevance: %v (%s) in %v", result.Relevant, result.Reason, time.Since(startTime))
		return &result, nil
	}

	resp, err := c.client.CreateChatCompletion(
		ctx,
		openai.ChatCompletionRequest{
			Model: model,
			Messages: []openai.ChatCompletionMessage{
				{Role: openai.ChatMessageRoleSystem, Content: systemBuf.String()},
				{Role: openai.ChatMessageRoleUser, Content: userBuf.String()},
			},
			Temperature: 0.1,
		},
	)
	if err != nil {
		return nil, err
	}

	jsonStr := extractJSONObject(resp.Choices[0].Message.Content)
	var result RelevanceResult
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, fmt.Errorf("failed to parse check_relevance JSON: %w", err)
	}
	log.Printf("CheckRelevance: %v (%s) in %v", result.Relevant, result.Reason, time.Since(startTime))
	return &result, nil
}

func (c *Client) CheckRedundancy(ctx context.Context, topic, existingArticle, newContent string) (*RedundancyResult, error) {
	startTime := time.Now()

	// Execute system template
	var systemBuf bytes.Buffer
	if err := c.checkRedundancySystemTemplate.Execute(&systemBuf, nil); err != nil {
		return nil, fmt.Errorf("failed to execute check_redundancy system template: %w", err)
	}

	// Execute user template
	data := map[string]interface{}{
		"Topic":           topic,
		"ExistingArticle": existingArticle,
		"NewContent":      newContent,
	}
	var userBuf bytes.Buffer
	if err := c.checkRedundancyUserTemplate.Execute(&userBuf, data); err != nil {
		return nil, fmt.Errorf("failed to execute check_redundancy user template: %w", err)
	}

	// Use fast model for checks
	model := c.modelSuggestTopics

	if c.ThinkingEnabled() {
		messages := []ollamaChatMessage{
			{Role: "system", Content: systemBuf.String()},
			{Role: "user", Content: userBuf.String()},
		}
		resp, err := c.chatWithThinking(ctx, model, messages, 0.1)
		if err != nil {
			return nil, err
		}

		jsonStr := extractJSONObject(resp.Message.Content)
		var result RedundancyResult
		if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
			return nil, fmt.Errorf("failed to parse check_redundancy JSON: %w", err)
		}
		log.Printf("CheckRedundancy: %v (%s) in %v", result.IsRedundant, result.Reason, time.Since(startTime))
		return &result, nil
	}

	resp, err := c.client.CreateChatCompletion(
		ctx,
		openai.ChatCompletionRequest{
			Model: model,
			Messages: []openai.ChatCompletionMessage{
				{Role: openai.ChatMessageRoleSystem, Content: systemBuf.String()},
				{Role: openai.ChatMessageRoleUser, Content: userBuf.String()},
			},
			Temperature: 0.1,
		},
	)
	if err != nil {
		return nil, err
	}

	jsonStr := extractJSONObject(resp.Choices[0].Message.Content)
	var result RedundancyResult
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, fmt.Errorf("failed to parse check_redundancy JSON: %w", err)
	}
	log.Printf("CheckRedundancy: %v (%s) in %v", result.IsRedundant, result.Reason, time.Since(startTime))
	return &result, nil
}

func (c *Client) IntegrateContent(ctx context.Context, topic, existingArticle, newContent string) (string, error) {
	startTime := time.Now()

	// Execute system template
	var systemBuf bytes.Buffer
	if err := c.integrateContentSystemTemplate.Execute(&systemBuf, nil); err != nil {
		return "", fmt.Errorf("failed to execute integrate_content system template: %w", err)
	}

	// Execute user template
	data := map[string]interface{}{
		"Topic":           topic,
		"ExistingArticle": existingArticle,
		"NewContent":      newContent,
	}
	var userBuf bytes.Buffer
	if err := c.integrateContentUserTemplate.Execute(&userBuf, data); err != nil {
		return "", fmt.Errorf("failed to execute integrate_content user template: %w", err)
	}

	// Use thinking mode if enabled - integration requires careful thought
	if c.ThinkingEnabled() {
		messages := []ollamaChatMessage{
			{Role: "system", Content: systemBuf.String()},
			{Role: "user", Content: userBuf.String()},
		}
		resp, err := c.chatWithThinking(ctx, c.modelGenerateArticle, messages, 0.3)
		if err != nil {
			return "", err
		}
		log.Printf("IntegrateContent: Completed in %v (%d chars)", time.Since(startTime), len(resp.Message.Content))
		return resp.Message.Content, nil
	}

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

	result := resp.Choices[0].Message.Content
	log.Printf("IntegrateContent: Completed in %v (%d chars)", time.Since(startTime), len(result))
	return result, nil
}
