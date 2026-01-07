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
	// Article improvement templates
	isEncyclopediaSourceSystemTemplate *template.Template
	isEncyclopediaSourceUserTemplate   *template.Template
	extractSectionsSystemTemplate      *template.Template
	extractSectionsUserTemplate        *template.Template
	compareSectionsSystemTemplate      *template.Template
	compareSectionsUserTemplate        *template.Template
	mergeSectionSystemTemplate              *template.Template
	mergeSectionUserTemplate                *template.Template
	scoreImprovementSystemTemplate          *template.Template
	scoreImprovementUserTemplate            *template.Template
	suggestNewSectionSystemTemplate         *template.Template
	suggestNewSectionUserTemplate           *template.Template
	generateSectionSearchQuerySystemTemplate *template.Template
	generateSectionSearchQueryUserTemplate   *template.Template
	// Image generation templates
	generateImagePromptSystemTemplate      *template.Template
	generateImagePromptUserTemplate        *template.Template
	extractVisualElementsSystemTemplate    *template.Template
	extractVisualElementsUserTemplate      *template.Template
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

	// Article improvement templates
	isEncyclopediaSourceSystem, err := loadTemplate("prompts/is_encyclopedia_source_system.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load is_encyclopedia_source_system template: %w", err)
	}

	isEncyclopediaSourceUser, err := loadTemplate("prompts/is_encyclopedia_source_user.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load is_encyclopedia_source_user template: %w", err)
	}

	extractSectionsSystem, err := loadTemplate("prompts/extract_sections_system.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load extract_sections_system template: %w", err)
	}

	extractSectionsUser, err := loadTemplate("prompts/extract_sections_user.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load extract_sections_user template: %w", err)
	}

	compareSectionsSystem, err := loadTemplate("prompts/compare_sections_system.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load compare_sections_system template: %w", err)
	}

	compareSectionsUser, err := loadTemplate("prompts/compare_sections_user.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load compare_sections_user template: %w", err)
	}

	mergeSectionSystem, err := loadTemplate("prompts/merge_section_system.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load merge_section_system template: %w", err)
	}

	mergeSectionUser, err := loadTemplate("prompts/merge_section_user.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load merge_section_user template: %w", err)
	}

	scoreImprovementSystem, err := loadTemplate("prompts/score_improvement_system.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load score_improvement_system template: %w", err)
	}

	scoreImprovementUser, err := loadTemplate("prompts/score_improvement_user.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load score_improvement_user template: %w", err)
	}

	// Context-aware search query templates
	suggestNewSectionSystem, err := loadTemplate("prompts/suggest_new_section_system.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load suggest_new_section_system template: %w", err)
	}

	suggestNewSectionUser, err := loadTemplate("prompts/suggest_new_section_user.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load suggest_new_section_user template: %w", err)
	}

	generateSectionSearchQuerySystem, err := loadTemplate("prompts/generate_section_search_query_system.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load generate_section_search_query_system template: %w", err)
	}

	generateSectionSearchQueryUser, err := loadTemplate("prompts/generate_section_search_query_user.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load generate_section_search_query_user template: %w", err)
	}

	// Image generation templates
	generateImagePromptSystem, err := loadTemplate("prompts/generate_image_prompt_system.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load generate_image_prompt_system template: %w", err)
	}

	generateImagePromptUser, err := loadTemplate("prompts/generate_image_prompt_user.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load generate_image_prompt_user template: %w", err)
	}

	// Visual elements extraction templates
	extractVisualElementsSystem, err := loadTemplate("prompts/extract_visual_elements_system.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load extract_visual_elements_system template: %w", err)
	}

	extractVisualElementsUser, err := loadTemplate("prompts/extract_visual_elements_user.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load extract_visual_elements_user template: %w", err)
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
		checkRedundancySystemTemplate:      checkRedundancySystem,
		checkRedundancyUserTemplate:        checkRedundancyUser,
		integrateContentSystemTemplate:     integrateContentSystem,
		integrateContentUserTemplate:       integrateContentUser,
		isEncyclopediaSourceSystemTemplate: isEncyclopediaSourceSystem,
		isEncyclopediaSourceUserTemplate:   isEncyclopediaSourceUser,
		extractSectionsSystemTemplate:      extractSectionsSystem,
		extractSectionsUserTemplate:        extractSectionsUser,
		compareSectionsSystemTemplate:      compareSectionsSystem,
		compareSectionsUserTemplate:        compareSectionsUser,
		mergeSectionSystemTemplate:               mergeSectionSystem,
		mergeSectionUserTemplate:                 mergeSectionUser,
		scoreImprovementSystemTemplate:           scoreImprovementSystem,
		scoreImprovementUserTemplate:             scoreImprovementUser,
		suggestNewSectionSystemTemplate:          suggestNewSectionSystem,
		suggestNewSectionUserTemplate:            suggestNewSectionUser,
		generateSectionSearchQuerySystemTemplate: generateSectionSearchQuerySystem,
		generateSectionSearchQueryUserTemplate:   generateSectionSearchQueryUser,
		generateImagePromptSystemTemplate:        generateImagePromptSystem,
		generateImagePromptUserTemplate:          generateImagePromptUser,
		extractVisualElementsSystemTemplate:      extractVisualElementsSystem,
		extractVisualElementsUserTemplate:        extractVisualElementsUser,
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

// isRefusalResponse checks if an LLM response is a refusal/failure message instead of actual content
func isRefusalResponse(content string) bool {
	lowerContent := strings.ToLower(content)
	refusalPatterns := []string{
		"unfortunately, i cannot provide",
		"i cannot provide",
		"i'm unable to provide",
		"i am unable to provide",
		"not_relevant",
		"i cannot generate",
		"i'm unable to generate",
		"i am unable to generate",
		"cannot create an article",
		"unable to create an article",
		"no relevant information",
		"insufficient information",
		"i cannot write",
		"i'm unable to write",
	}
	for _, pattern := range refusalPatterns {
		if strings.Contains(lowerContent, pattern) {
			return true
		}
	}
	return false
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

	var result string

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
		result = resp.Message.Content
	} else {
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
		result = resp.Choices[0].Message.Content
	}

	// Validate the response - check for refusal messages
	if isRefusalResponse(result) {
		log.Printf("GenerateMiniArticle: LLM returned refusal response for topic '%s', rejecting", topic)
		return "", fmt.Errorf("LLM refused to generate article: response indicates insufficient or irrelevant source content")
	}

	// Check for minimum content length (frontmatter + at least some content)
	if len(result) < 200 {
		log.Printf("GenerateMiniArticle: Response too short (%d chars) for topic '%s', rejecting", len(result), topic)
		return "", fmt.Errorf("LLM generated insufficient content: only %d characters", len(result))
	}

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

// IsEncyclopediaSource checks if a URL is from an encyclopedia-style website
func (c *Client) IsEncyclopediaSource(ctx context.Context, domain, url, title string) (*EncyclopediaCheckResult, error) {
	startTime := time.Now()

	var systemBuf bytes.Buffer
	if err := c.isEncyclopediaSourceSystemTemplate.Execute(&systemBuf, nil); err != nil {
		return nil, fmt.Errorf("failed to execute is_encyclopedia_source system template: %w", err)
	}

	data := map[string]interface{}{
		"Domain": domain,
		"URL":    url,
		"Title":  title,
	}
	var userBuf bytes.Buffer
	if err := c.isEncyclopediaSourceUserTemplate.Execute(&userBuf, data); err != nil {
		return nil, fmt.Errorf("failed to execute is_encyclopedia_source user template: %w", err)
	}

	// Use fast model for classification
	resp, err := c.client.CreateChatCompletion(
		ctx,
		openai.ChatCompletionRequest{
			Model: c.modelSuggestTopics,
			Messages: []openai.ChatCompletionMessage{
				{Role: openai.ChatMessageRoleSystem, Content: systemBuf.String()},
				{Role: openai.ChatMessageRoleUser, Content: userBuf.String()},
			},
			Temperature: 0.0,
		},
	)
	if err != nil {
		return nil, err
	}

	jsonStr := extractJSONObject(resp.Choices[0].Message.Content)
	var result EncyclopediaCheckResult
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, fmt.Errorf("failed to parse is_encyclopedia JSON: %w", err)
	}
	log.Printf("IsEncyclopediaSource: %s -> %v (%s) in %v", domain, result.IsEncyclopedia, result.Reason, time.Since(startTime))
	return &result, nil
}

// ExtractSections extracts section headings from an article
func (c *Client) ExtractSections(ctx context.Context, article string) ([]ArticleSection, error) {
	startTime := time.Now()

	var systemBuf bytes.Buffer
	if err := c.extractSectionsSystemTemplate.Execute(&systemBuf, nil); err != nil {
		return nil, fmt.Errorf("failed to execute extract_sections system template: %w", err)
	}

	data := map[string]interface{}{
		"Article": article,
	}
	var userBuf bytes.Buffer
	if err := c.extractSectionsUserTemplate.Execute(&userBuf, data); err != nil {
		return nil, fmt.Errorf("failed to execute extract_sections user template: %w", err)
	}

	resp, err := c.client.CreateChatCompletion(
		ctx,
		openai.ChatCompletionRequest{
			Model: c.modelSuggestTopics,
			Messages: []openai.ChatCompletionMessage{
				{Role: openai.ChatMessageRoleSystem, Content: systemBuf.String()},
				{Role: openai.ChatMessageRoleUser, Content: userBuf.String()},
			},
			Temperature: 0.0,
		},
	)
	if err != nil {
		return nil, err
	}

	jsonStr, found := extractJSONArray(resp.Choices[0].Message.Content)
	if !found {
		return nil, fmt.Errorf("failed to find JSON array in extract_sections response")
	}

	var sections []ArticleSection
	if err := json.Unmarshal([]byte(jsonStr), &sections); err != nil {
		return nil, fmt.Errorf("failed to parse extract_sections JSON: %w", err)
	}
	log.Printf("ExtractSections: Found %d sections in %v", len(sections), time.Since(startTime))
	return sections, nil
}

// CompareSections compares two articles and suggests if a new section should be added
func (c *Client) CompareSections(ctx context.Context, topic, existingArticle, existingSections, newArticle, newSections string) (*SectionComparisonResult, error) {
	startTime := time.Now()

	var systemBuf bytes.Buffer
	if err := c.compareSectionsSystemTemplate.Execute(&systemBuf, nil); err != nil {
		return nil, fmt.Errorf("failed to execute compare_sections system template: %w", err)
	}

	data := map[string]interface{}{
		"Topic":            topic,
		"ExistingArticle":  existingArticle,
		"ExistingSections": existingSections,
		"NewArticle":       newArticle,
		"NewSections":      newSections,
	}
	var userBuf bytes.Buffer
	if err := c.compareSectionsUserTemplate.Execute(&userBuf, data); err != nil {
		return nil, fmt.Errorf("failed to execute compare_sections user template: %w", err)
	}

	// Use article model for thorough comparison
	if c.ThinkingEnabled() {
		messages := []ollamaChatMessage{
			{Role: "system", Content: systemBuf.String()},
			{Role: "user", Content: userBuf.String()},
		}
		resp, err := c.chatWithThinking(ctx, c.modelGenerateArticle, messages, 0.3)
		if err != nil {
			return nil, err
		}
		jsonStr := extractJSONObject(resp.Message.Content)
		var result SectionComparisonResult
		if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
			return nil, fmt.Errorf("failed to parse compare_sections JSON: %w", err)
		}
		log.Printf("CompareSections: hasNewSection=%v in %v", result.HasNewSection, time.Since(startTime))
		return &result, nil
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
		return nil, err
	}

	jsonStr := extractJSONObject(resp.Choices[0].Message.Content)
	var result SectionComparisonResult
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, fmt.Errorf("failed to parse compare_sections JSON: %w", err)
	}
	log.Printf("CompareSections: hasNewSection=%v in %v", result.HasNewSection, time.Since(startTime))
	return &result, nil
}

// MergeSection combines two versions of a section
func (c *Client) MergeSection(ctx context.Context, topic, sectionTitle, currentSection, newContent string) (string, error) {
	startTime := time.Now()

	var systemBuf bytes.Buffer
	if err := c.mergeSectionSystemTemplate.Execute(&systemBuf, nil); err != nil {
		return "", fmt.Errorf("failed to execute merge_section system template: %w", err)
	}

	data := map[string]interface{}{
		"Topic":          topic,
		"SectionTitle":   sectionTitle,
		"CurrentSection": currentSection,
		"NewContent":     newContent,
	}
	var userBuf bytes.Buffer
	if err := c.mergeSectionUserTemplate.Execute(&userBuf, data); err != nil {
		return "", fmt.Errorf("failed to execute merge_section user template: %w", err)
	}

	if c.ThinkingEnabled() {
		messages := []ollamaChatMessage{
			{Role: "system", Content: systemBuf.String()},
			{Role: "user", Content: userBuf.String()},
		}
		resp, err := c.chatWithThinking(ctx, c.modelGenerateArticle, messages, 0.3)
		if err != nil {
			return "", err
		}
		log.Printf("MergeSection: Completed in %v (%d chars)", time.Since(startTime), len(resp.Message.Content))
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
	log.Printf("MergeSection: Completed in %v (%d chars)", time.Since(startTime), len(result))
	return result, nil
}

// ScoreImprovement evaluates if a revised section is a meaningful improvement
func (c *Client) ScoreImprovement(ctx context.Context, topic, sectionTitle, originalSection, revisedSection string) (*ImprovementScore, error) {
	startTime := time.Now()

	var systemBuf bytes.Buffer
	if err := c.scoreImprovementSystemTemplate.Execute(&systemBuf, nil); err != nil {
		return nil, fmt.Errorf("failed to execute score_improvement system template: %w", err)
	}

	data := map[string]interface{}{
		"Topic":           topic,
		"SectionTitle":    sectionTitle,
		"OriginalSection": originalSection,
		"RevisedSection":  revisedSection,
	}
	var userBuf bytes.Buffer
	if err := c.scoreImprovementUserTemplate.Execute(&userBuf, data); err != nil {
		return nil, fmt.Errorf("failed to execute score_improvement user template: %w", err)
	}

	resp, err := c.client.CreateChatCompletion(
		ctx,
		openai.ChatCompletionRequest{
			Model: c.modelSuggestTopics, // Use fast model for scoring
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
	var result ImprovementScore
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, fmt.Errorf("failed to parse score_improvement JSON: %w", err)
	}
	log.Printf("ScoreImprovement: score=%d, recommendation=%s in %v", result.Score, result.Recommendation, time.Since(startTime))
	return &result, nil
}

// SuggestNewSection asks the LLM to suggest a new section to add to an article
func (c *Client) SuggestNewSection(ctx context.Context, category, subcategory, topic string, existingSections []ArticleSection) (*SuggestSectionResult, error) {
	startTime := time.Now()

	var systemBuf bytes.Buffer
	if err := c.suggestNewSectionSystemTemplate.Execute(&systemBuf, nil); err != nil {
		return nil, fmt.Errorf("failed to execute suggest_new_section system template: %w", err)
	}

	// Format existing sections for the prompt
	var sectionsStr string
	for _, s := range existingSections {
		prefix := strings.Repeat("#", s.Level) + " "
		sectionsStr += fmt.Sprintf("%s%s\n", prefix, s.Title)
	}

	data := map[string]interface{}{
		"Category":    category,
		"Subcategory": subcategory,
		"Topic":       topic,
		"Sections":    sectionsStr,
	}
	var userBuf bytes.Buffer
	if err := c.suggestNewSectionUserTemplate.Execute(&userBuf, data); err != nil {
		return nil, fmt.Errorf("failed to execute suggest_new_section user template: %w", err)
	}

	resp, err := c.client.CreateChatCompletion(
		ctx,
		openai.ChatCompletionRequest{
			Model: c.modelSuggestTopics, // Use fast model
			Messages: []openai.ChatCompletionMessage{
				{Role: openai.ChatMessageRoleSystem, Content: systemBuf.String()},
				{Role: openai.ChatMessageRoleUser, Content: userBuf.String()},
			},
			Temperature: 0.3,
		},
	)
	if err != nil {
		return nil, err
	}

	jsonStr := extractJSONObject(resp.Choices[0].Message.Content)
	var result SuggestSectionResult
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, fmt.Errorf("failed to parse suggest_new_section JSON: %w", err)
	}
	log.Printf("SuggestNewSection: suggested '%s' after '%s', query='%s' in %v",
		result.SectionTitle, result.InsertAfter, result.SearchQuery, time.Since(startTime))
	return &result, nil
}

// GenerateSectionSearchQuery asks the LLM to generate a search query for improving a section
func (c *Client) GenerateSectionSearchQuery(ctx context.Context, category, subcategory, topic, sectionTitle, contentSummary string) (*SearchQueryResult, error) {
	startTime := time.Now()

	var systemBuf bytes.Buffer
	if err := c.generateSectionSearchQuerySystemTemplate.Execute(&systemBuf, nil); err != nil {
		return nil, fmt.Errorf("failed to execute generate_section_search_query system template: %w", err)
	}

	// Truncate content summary if too long
	if len(contentSummary) > 500 {
		contentSummary = contentSummary[:500] + "..."
	}

	data := map[string]interface{}{
		"Category":       category,
		"Subcategory":    subcategory,
		"Topic":          topic,
		"SectionTitle":   sectionTitle,
		"ContentSummary": contentSummary,
	}
	var userBuf bytes.Buffer
	if err := c.generateSectionSearchQueryUserTemplate.Execute(&userBuf, data); err != nil {
		return nil, fmt.Errorf("failed to execute generate_section_search_query user template: %w", err)
	}

	resp, err := c.client.CreateChatCompletion(
		ctx,
		openai.ChatCompletionRequest{
			Model: c.modelSuggestTopics, // Use fast model
			Messages: []openai.ChatCompletionMessage{
				{Role: openai.ChatMessageRoleSystem, Content: systemBuf.String()},
				{Role: openai.ChatMessageRoleUser, Content: userBuf.String()},
			},
			Temperature: 0.2,
		},
	)
	if err != nil {
		return nil, err
	}

	jsonStr := extractJSONObject(resp.Choices[0].Message.Content)
	var result SearchQueryResult
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, fmt.Errorf("failed to parse generate_section_search_query JSON: %w", err)
	}
	log.Printf("GenerateSectionSearchQuery: query='%s' in %v", result.SearchQuery, time.Since(startTime))
	return &result, nil
}

// ExtractVisualElements extracts article-specific visual concepts for image generation
func (c *Client) ExtractVisualElements(ctx context.Context, req VisualElementsRequest) (*VisualElements, error) {
	startTime := time.Now()

	// Execute system template
	var systemBuf bytes.Buffer
	if err := c.extractVisualElementsSystemTemplate.Execute(&systemBuf, nil); err != nil {
		return nil, fmt.Errorf("failed to execute extract_visual_elements system template: %w", err)
	}

	// Execute user template with article data
	userData := map[string]interface{}{
		"Topic":          req.Topic,
		"Category":       req.Category,
		"Subcategory":    req.Subcategory,
		"ArticleContent": req.ArticleContent,
	}
	var userBuf bytes.Buffer
	if err := c.extractVisualElementsUserTemplate.Execute(&userBuf, userData); err != nil {
		return nil, fmt.Errorf("failed to execute extract_visual_elements user template: %w", err)
	}

	log.Printf("ExtractVisualElements: Extracting visual elements for '%s' (%s > %s)", req.Topic, req.Category, req.Subcategory)

	// Use faster model for extraction (it's a structured task)
	resp, err := c.client.CreateChatCompletion(
		ctx,
		openai.ChatCompletionRequest{
			Model: c.modelSuggestTopics, // Use fast model
			Messages: []openai.ChatCompletionMessage{
				{Role: openai.ChatMessageRoleSystem, Content: systemBuf.String()},
				{Role: openai.ChatMessageRoleUser, Content: userBuf.String()},
			},
			Temperature: 0.3, // Lower temperature for more consistent extraction
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to extract visual elements: %w", err)
	}

	// Parse JSON response
	jsonStr := extractJSONObject(resp.Choices[0].Message.Content)
	var result VisualElements
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		log.Printf("ExtractVisualElements: Failed to parse JSON, raw response: %s", resp.Choices[0].Message.Content)
		// Return empty result rather than failing completely
		result = VisualElements{
			KeyConcepts:       []string{},
			SpecificPhenomena: []string{},
			NotableFigures:    []string{},
			IconicImagery:     []string{},
			MathElements:      []string{},
		}
	}

	log.Printf("ExtractVisualElements: Extracted %d concepts, %d phenomena, %d figures, %d imagery, %d math elements in %v",
		len(result.KeyConcepts), len(result.SpecificPhenomena), len(result.NotableFigures),
		len(result.IconicImagery), len(result.MathElements), time.Since(startTime))

	return &result, nil
}

// GenerateImagePrompt generates an image generation prompt for an article header
func (c *Client) GenerateImagePrompt(ctx context.Context, req ImagePromptRequest) (*ImagePromptResult, error) {
	startTime := time.Now()

	// Ensure we have valid extracted elements (use empty struct if nil)
	extractedElements := req.ExtractedElements
	if extractedElements == nil {
		extractedElements = &VisualElements{}
	}

	// Execute system template with category guidance
	systemData := map[string]interface{}{
		"CategoryGuidance": req.CategoryGuidance,
	}
	var systemBuf bytes.Buffer
	if err := c.generateImagePromptSystemTemplate.Execute(&systemBuf, systemData); err != nil {
		return nil, fmt.Errorf("failed to execute generate_image_prompt system template: %w", err)
	}

	// Execute user template with structured extraction data
	userData := map[string]interface{}{
		"Topic":             req.Topic,
		"Category":          req.Category,
		"Subcategory":       req.Subcategory,
		"ArticleSummary":    req.ArticleSummary,
		"ExtractedElements": extractedElements, // Pass full structured extraction
		"ColorMood":         req.ColorMood,
		"ArtisticStyles":    req.ArtisticStyles, // Pass as slice for template iteration
	}
	var userBuf bytes.Buffer
	if err := c.generateImagePromptUserTemplate.Execute(&userBuf, userData); err != nil {
		return nil, fmt.Errorf("failed to execute generate_image_prompt user template: %w", err)
	}

	log.Printf("GenerateImagePrompt: Generating prompt for '%s' (%s > %s)", req.Topic, req.Category, req.Subcategory)

	var result ImagePromptResult
	result.Model = c.modelGenerateArticle

	// Use thinking mode if enabled for better creative output
	if c.ThinkingEnabled() {
		messages := []ollamaChatMessage{
			{Role: "system", Content: systemBuf.String()},
			{Role: "user", Content: userBuf.String()},
		}
		resp, err := c.chatWithThinking(ctx, c.modelGenerateArticle, messages, 0.7)
		if err != nil {
			return nil, fmt.Errorf("failed to generate image prompt: %w", err)
		}
		result.Prompt = strings.TrimSpace(resp.Message.Content)
		result.Thinking = resp.Message.Thinking
	} else {
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
			return nil, fmt.Errorf("failed to generate image prompt: %w", err)
		}
		result.Prompt = strings.TrimSpace(resp.Choices[0].Message.Content)
	}

	// Clean up the prompt - remove any markdown or extra formatting
	result.Prompt = cleanImagePrompt(result.Prompt)

	log.Printf("GenerateImagePrompt: Generated prompt in %v (%d chars)", time.Since(startTime), len(result.Prompt))
	return &result, nil
}

// cleanImagePrompt removes any markdown formatting or extra text from the generated prompt
func cleanImagePrompt(prompt string) string {
	// Remove markdown code blocks if present
	prompt = strings.TrimSpace(prompt)
	if strings.HasPrefix(prompt, "```") {
		lines := strings.Split(prompt, "\n")
		var cleanLines []string
		inCodeBlock := false
		for _, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "```") {
				inCodeBlock = !inCodeBlock
				continue
			}
			if !inCodeBlock || (inCodeBlock && !strings.HasPrefix(strings.TrimSpace(line), "```")) {
				cleanLines = append(cleanLines, line)
			}
		}
		prompt = strings.Join(cleanLines, "\n")
	}

	// Remove any leading "Prompt:" or similar labels
	prompt = strings.TrimSpace(prompt)
	prefixes := []string{"Prompt:", "Image prompt:", "Final prompt:", "Output:"}
	for _, prefix := range prefixes {
		if strings.HasPrefix(strings.ToLower(prompt), strings.ToLower(prefix)) {
			prompt = strings.TrimSpace(prompt[len(prefix):])
		}
	}

	// Remove surrounding quotes if present
	if (strings.HasPrefix(prompt, "\"") && strings.HasSuffix(prompt, "\"")) ||
		(strings.HasPrefix(prompt, "'") && strings.HasSuffix(prompt, "'")) {
		prompt = prompt[1 : len(prompt)-1]
	}

	return strings.TrimSpace(prompt)
}
