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
	modelSuggestTopics                string
	modelSummarizePlain               string
	modelSummarizeJSON                string
	thinkMode                         string // "false", "true", "low", "medium", "high"
	ollamaBaseUrl                     string
	generateArticleSystemTemplate     *template.Template
	generateArticleUserTemplate       *template.Template
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
	checkRedundancySystemTemplate            *template.Template
	checkRedundancyUserTemplate              *template.Template
	integrateContentSystemTemplate           *template.Template
	integrateContentUserTemplate             *template.Template
	extractVisualElementsSystemTemplate      *template.Template
	extractVisualElementsUserTemplate        *template.Template
	generateImagePromptSystemTemplate        *template.Template
	generateImagePromptUserTemplate          *template.Template
	evaluateSectionImageSystemTemplate           *template.Template
	evaluateSectionImageUserTemplate             *template.Template
	generateSectionImagePromptSystemTemplate     *template.Template
	generateSectionImagePromptUserTemplate       *template.Template
	isEncyclopediaSourceSystemTemplate           *template.Template
	isEncyclopediaSourceUserTemplate             *template.Template
	extractSectionsSystemTemplate                *template.Template
	extractSectionsUserTemplate                  *template.Template
	suggestNewSectionSystemTemplate              *template.Template
	suggestNewSectionUserTemplate                *template.Template
	compareSectionsSystemTemplate                *template.Template
	compareSectionsUserTemplate                  *template.Template
	orderSectionsSystemTemplate  *template.Template
	orderSectionsUserTemplate    *template.Template
	mergeSectionSystemTemplate               *template.Template
	mergeSectionUserTemplate                 *template.Template
	scoreImprovementSystemTemplate           *template.Template
	scoreImprovementUserTemplate             *template.Template
	extractConceptsSystemTemplate            *template.Template
	extractConceptsUserTemplate              *template.Template
	mapConceptToSectionSystemTemplate        *template.Template
	mapConceptToSectionUserTemplate          *template.Template
	rewriteSectionWithConceptSystemTemplate  *template.Template
	rewriteSectionWithConceptUserTemplate    *template.Template
	generateNewSectionSystemTemplate         *template.Template
	generateNewSectionUserTemplate           *template.Template
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
	var modelGenerateArticle, modelSuggestTopics, modelSummarizePlain, modelSummarizeJSON string

	// Legacy fallback
	if legacyModel := os.Getenv("LLM_MODEL"); legacyModel != "" {
		modelGenerateArticle = legacyModel
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

		// Article generation: large model for reliability
		modelArticle := os.Getenv("LLM_MODEL_ARTICLE")
		if modelArticle == "" {
			modelArticle = "qwen3:32b"
		}

		// Assign models to tasks
		modelGenerateArticle = modelArticle
		modelSuggestTopics = modelFast
		modelSummarizePlain = modelArticle // Use large model for comprehensive summarization
		modelSummarizeJSON = modelFast     // Use fast model for JSON conversion

		log.Printf("Multi-model configuration: Fast=%s, Article/Summarize=%s", modelFast, modelArticle)
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

	extractVisualElementsSystem, err := loadTemplate("prompts/extract_visual_elements_system.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load extract_visual_elements_system template: %w", err)
	}

	extractVisualElementsUser, err := loadTemplate("prompts/extract_visual_elements_user.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load extract_visual_elements_user template: %w", err)
	}

	generateImagePromptSystem, err := loadTemplate("prompts/generate_image_prompt_system.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load generate_image_prompt_system template: %w", err)
	}

	generateImagePromptUser, err := loadTemplate("prompts/generate_image_prompt_user.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load generate_image_prompt_user template: %w", err)
	}

	evaluateSectionImageSystem, err := loadTemplate("prompts/evaluate_section_image_system.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load evaluate_section_image_system template: %w", err)
	}

	evaluateSectionImageUser, err := loadTemplate("prompts/evaluate_section_image_user.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load evaluate_section_image_user template: %w", err)
	}

	generateSectionImagePromptSystem, err := loadTemplate("prompts/generate_section_image_prompt_system.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load generate_section_image_prompt_system template: %w", err)
	}

	generateSectionImagePromptUser, err := loadTemplate("prompts/generate_section_image_prompt_user.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load generate_section_image_prompt_user template: %w", err)
	}

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

	suggestNewSectionSystem, err := loadTemplate("prompts/suggest_new_section_system.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load suggest_new_section_system template: %w", err)
	}

	suggestNewSectionUser, err := loadTemplate("prompts/suggest_new_section_user.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load suggest_new_section_user template: %w", err)
	}

	compareSectionsSystem, err := loadTemplate("prompts/compare_sections_system.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load compare_sections_system template: %w", err)
	}

	compareSectionsUser, err := loadTemplate("prompts/compare_sections_user.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load compare_sections_user template: %w", err)
	}

	orderSectionsSystem, err := loadTemplate("prompts/order_sections_system.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load order_sections_system template: %w", err)
	}

	orderSectionsUser, err := loadTemplate("prompts/order_sections_user.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load order_sections_user template: %w", err)
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

	extractConceptsSystem, err := loadTemplate("prompts/extract_concepts_system.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load extract_concepts_system template: %w", err)
	}

	extractConceptsUser, err := loadTemplate("prompts/extract_concepts_user.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load extract_concepts_user template: %w", err)
	}

	mapConceptToSectionSystem, err := loadTemplate("prompts/map_concept_to_section_system.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load map_concept_to_section_system template: %w", err)
	}

	mapConceptToSectionUser, err := loadTemplate("prompts/map_concept_to_section_user.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load map_concept_to_section_user template: %w", err)
	}

	rewriteSectionWithConceptSystem, err := loadTemplate("prompts/rewrite_section_with_concept_system.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load rewrite_section_with_concept_system template: %w", err)
	}

	rewriteSectionWithConceptUser, err := loadTemplate("prompts/rewrite_section_with_concept_user.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load rewrite_section_with_concept_user template: %w", err)
	}

	generateNewSectionSystem, err := loadTemplate("prompts/generate_new_section_system.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load generate_new_section_system template: %w", err)
	}

	generateNewSectionUser, err := loadTemplate("prompts/generate_new_section_user.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load generate_new_section_user template: %w", err)
	}

	return &Client{
		client:                            openai.NewClientWithConfig(config),
		httpClient:                        httpClient,
		modelGenerateArticle:              modelGenerateArticle,
		modelSuggestTopics:                modelSuggestTopics,
		modelSummarizePlain:               modelSummarizePlain,
		modelSummarizeJSON:                modelSummarizeJSON,
		thinkMode:                         thinkMode,
		ollamaBaseUrl:                     ollamaBaseUrl,
		generateArticleSystemTemplate:     generateArticleSystem,
		generateArticleUserTemplate:       generateArticleUser,
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
		checkRedundancySystemTemplate:            checkRedundancySystem,
		checkRedundancyUserTemplate:              checkRedundancyUser,
		integrateContentSystemTemplate:           integrateContentSystem,
		integrateContentUserTemplate:             integrateContentUser,
		extractVisualElementsSystemTemplate:      extractVisualElementsSystem,
		extractVisualElementsUserTemplate:        extractVisualElementsUser,
		generateImagePromptSystemTemplate:        generateImagePromptSystem,
		generateImagePromptUserTemplate:          generateImagePromptUser,
		evaluateSectionImageSystemTemplate:       evaluateSectionImageSystem,
		evaluateSectionImageUserTemplate:         evaluateSectionImageUser,
		generateSectionImagePromptSystemTemplate:     generateSectionImagePromptSystem,
		generateSectionImagePromptUserTemplate:       generateSectionImagePromptUser,
		isEncyclopediaSourceSystemTemplate:           isEncyclopediaSourceSystem,
		isEncyclopediaSourceUserTemplate:             isEncyclopediaSourceUser,
		extractSectionsSystemTemplate:                extractSectionsSystem,
		extractSectionsUserTemplate:                  extractSectionsUser,
		suggestNewSectionSystemTemplate:              suggestNewSectionSystem,
		suggestNewSectionUserTemplate:                suggestNewSectionUser,
		compareSectionsSystemTemplate:                compareSectionsSystem,
		compareSectionsUserTemplate:                  compareSectionsUser,
		orderSectionsSystemTemplate:  orderSectionsSystem,
		orderSectionsUserTemplate:    orderSectionsUser,
		mergeSectionSystemTemplate:              mergeSectionSystem,
		mergeSectionUserTemplate:                mergeSectionUser,
		scoreImprovementSystemTemplate:          scoreImprovementSystem,
		scoreImprovementUserTemplate:            scoreImprovementUser,
		extractConceptsSystemTemplate:           extractConceptsSystem,
		extractConceptsUserTemplate:             extractConceptsUser,
		mapConceptToSectionSystemTemplate:       mapConceptToSectionSystem,
		mapConceptToSectionUserTemplate:         mapConceptToSectionUser,
		rewriteSectionWithConceptSystemTemplate: rewriteSectionWithConceptSystem,
		rewriteSectionWithConceptUserTemplate:   rewriteSectionWithConceptUser,
		generateNewSectionSystemTemplate:        generateNewSectionSystem,
		generateNewSectionUserTemplate:          generateNewSectionUser,
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

	// Step 1: Summarize content to plain text
	var plain string

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

	// IMPORTANT: Do NOT use thinking mode for summarization - it consumes output tokens
	// and causes excessive compression. Experiments show ~3x longer output without thinking.
	{
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
	if false { // Keep old code path for reference
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

// ExtractVisualElements extracts visual concepts from an article for image generation
func (c *Client) ExtractVisualElements(ctx context.Context, req VisualElementsRequest) (*VisualElements, error) {
	startTime := time.Now()

	// Execute system template
	var systemBuf bytes.Buffer
	if err := c.extractVisualElementsSystemTemplate.Execute(&systemBuf, nil); err != nil {
		return nil, fmt.Errorf("failed to execute extract_visual_elements system template: %w", err)
	}

	// Execute user template
	data := map[string]interface{}{
		"Topic":          req.Topic,
		"Category":       req.Category,
		"Subcategory":    req.Subcategory,
		"ArticleContent": req.ArticleContent,
	}
	var userBuf bytes.Buffer
	if err := c.extractVisualElementsUserTemplate.Execute(&userBuf, data); err != nil {
		return nil, fmt.Errorf("failed to execute extract_visual_elements user template: %w", err)
	}

	// Use fast model for JSON extraction
	resp, err := c.client.CreateChatCompletion(
		ctx,
		openai.ChatCompletionRequest{
			Model: c.modelSummarizeJSON,
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
	var result VisualElements
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, fmt.Errorf("failed to parse visual elements JSON: %w", err)
	}

	log.Printf("ExtractVisualElements: Extracted %d concepts, %d phenomena, %d figures in %v",
		len(result.KeyConcepts), len(result.SpecificPhenomena), len(result.NotableFigures), time.Since(startTime))
	return &result, nil
}

// GenerateImagePrompt generates an image prompt for an article header
func (c *Client) GenerateImagePrompt(ctx context.Context, req ImagePromptRequest) (*ImagePromptResult, error) {
	startTime := time.Now()

	// Execute system template with category guidance
	systemData := map[string]interface{}{
		"CategoryGuidance": req.CategoryGuidance,
	}
	var systemBuf bytes.Buffer
	if err := c.generateImagePromptSystemTemplate.Execute(&systemBuf, systemData); err != nil {
		return nil, fmt.Errorf("failed to execute generate_image_prompt system template: %w", err)
	}

	// Execute user template
	data := map[string]interface{}{
		"Topic":             req.Topic,
		"Category":          req.Category,
		"Subcategory":       req.Subcategory,
		"ArticleSummary":    req.ArticleSummary,
		"ExtractedElements": req.ExtractedElements,
		"ColorMood":         req.ColorMood,
		"ArtisticStyles":    req.ArtisticStyles,
	}
	var userBuf bytes.Buffer
	if err := c.generateImagePromptUserTemplate.Execute(&userBuf, data); err != nil {
		return nil, fmt.Errorf("failed to execute generate_image_prompt user template: %w", err)
	}

	// Use thinking mode for creative generation
	if c.ThinkingEnabled() {
		messages := []ollamaChatMessage{
			{Role: "system", Content: systemBuf.String()},
			{Role: "user", Content: userBuf.String()},
		}
		resp, err := c.chatWithThinking(ctx, c.modelGenerateArticle, messages, 0.7)
		if err != nil {
			return nil, err
		}
		log.Printf("GenerateImagePrompt: Generated in %v (%d chars)", time.Since(startTime), len(resp.Message.Content))
		return &ImagePromptResult{
			Prompt:   strings.TrimSpace(resp.Message.Content),
			Model:    c.modelGenerateArticle,
			Thinking: resp.Message.Thinking,
		}, nil
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
		return nil, err
	}

	result := strings.TrimSpace(resp.Choices[0].Message.Content)
	log.Printf("GenerateImagePrompt: Generated in %v (%d chars)", time.Since(startTime), len(result))
	return &ImagePromptResult{
		Prompt: result,
		Model:  c.modelGenerateArticle,
	}, nil
}

// EvaluateSectionImage evaluates a section for image suitability
func (c *Client) EvaluateSectionImage(ctx context.Context, req SectionImageEvaluationRequest) (*SectionImageEvaluationResult, error) {
	startTime := time.Now()

	// Execute system template
	var systemBuf bytes.Buffer
	if err := c.evaluateSectionImageSystemTemplate.Execute(&systemBuf, nil); err != nil {
		return nil, fmt.Errorf("failed to execute evaluate_section_image system template: %w", err)
	}

	// Execute user template
	data := map[string]interface{}{
		"ArticleTitle":   req.ArticleTitle,
		"SectionTitle":   req.SectionTitle,
		"SectionContent": req.SectionContent,
		"Category":       req.Category,
		"Subcategory":    req.Subcategory,
	}
	var userBuf bytes.Buffer
	if err := c.evaluateSectionImageUserTemplate.Execute(&userBuf, data); err != nil {
		return nil, fmt.Errorf("failed to execute evaluate_section_image user template: %w", err)
	}

	// Use fast model for evaluation
	resp, err := c.client.CreateChatCompletion(
		ctx,
		openai.ChatCompletionRequest{
			Model: c.modelSummarizeJSON,
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
	var result SectionImageEvaluationResult
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, fmt.Errorf("failed to parse section image evaluation JSON: %w", err)
	}

	log.Printf("EvaluateSectionImage: %s scored %d for '%s' in %v",
		result.RecommendedType, result.RecommendedScore, req.SectionTitle, time.Since(startTime))
	return &result, nil
}

// GenerateSectionImagePrompt generates an image prompt for a section
func (c *Client) GenerateSectionImagePrompt(ctx context.Context, req SectionImagePromptRequest) (*SectionImagePromptResult, error) {
	startTime := time.Now()

	// Execute system template
	var systemBuf bytes.Buffer
	if err := c.generateSectionImagePromptSystemTemplate.Execute(&systemBuf, nil); err != nil {
		return nil, fmt.Errorf("failed to execute generate_section_image_prompt system template: %w", err)
	}

	// Execute user template
	data := map[string]interface{}{
		"ArticleTitle":         req.ArticleTitle,
		"SectionTitle":         req.SectionTitle,
		"SectionContent":       req.SectionContent,
		"Category":             req.Category,
		"Subcategory":          req.Subcategory,
		"ImageType":            req.ImageType,
		"ArtisticStyle":        req.ArtisticStyle,
		"KeyElements":          req.KeyElements,
		"DiagramSpecification": req.DiagramSpecification,
	}
	var userBuf bytes.Buffer
	if err := c.generateSectionImagePromptUserTemplate.Execute(&userBuf, data); err != nil {
		return nil, fmt.Errorf("failed to execute generate_section_image_prompt user template: %w", err)
	}

	// Use article model for creative generation
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
		return nil, err
	}

	result := strings.TrimSpace(resp.Choices[0].Message.Content)
	log.Printf("GenerateSectionImagePrompt: Generated %s prompt in %v (%d chars)",
		req.ImageType, time.Since(startTime), len(result))
	return &SectionImagePromptResult{
		Prompt: result,
		Model:  c.modelGenerateArticle,
	}, nil
}

// IsEncyclopediaSource checks if a source URL is from an encyclopedia site
func (c *Client) IsEncyclopediaSource(ctx context.Context, domain, url, title string) (*EncyclopediaCheckResult, error) {
	startTime := time.Now()

	// Simple heuristic check for common encyclopedia domains
	encyclopediaDomains := []string{
		"wikipedia.org",
		"britannica.com",
		"encyclopedia.com",
		"scholarpedia.org",
		"wikiwand.com",
	}

	for _, encDomain := range encyclopediaDomains {
		if strings.Contains(strings.ToLower(domain), encDomain) {
			log.Printf("IsEncyclopediaSource: %s matched known encyclopedia domain in %v", domain, time.Since(startTime))
			return &EncyclopediaCheckResult{
				IsEncyclopedia: true,
				Reason:         fmt.Sprintf("Domain %s is a known encyclopedia site", domain),
			}, nil
		}
	}

	// For other domains, return false (could be enhanced with LLM check if needed)
	log.Printf("IsEncyclopediaSource: %s is not a known encyclopedia in %v", domain, time.Since(startTime))
	return &EncyclopediaCheckResult{
		IsEncyclopedia: false,
		Reason:         "Domain is not a known encyclopedia site",
	}, nil
}

// ExtractSections extracts sections from article markdown content
func (c *Client) ExtractSections(ctx context.Context, articleContent string) ([]ArticleSection, error) {
	startTime := time.Now()

	lines := strings.Split(articleContent, "\n")
	var sections []ArticleSection
	var currentSection *ArticleSection

	// Skip frontmatter
	inFrontmatter := false
	contentStart := 0
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			if !inFrontmatter {
				inFrontmatter = true
			} else {
				contentStart = i + 1
				break
			}
		}
	}

	for i := contentStart; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// Check for heading
		if strings.HasPrefix(trimmed, "#") {
			// Save previous section
			if currentSection != nil {
				currentSection.Content = strings.TrimSpace(currentSection.Content)
				sections = append(sections, *currentSection)
			}

			// Determine heading level
			level := 0
			for _, ch := range trimmed {
				if ch == '#' {
					level++
				} else {
					break
				}
			}

			title := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
			currentSection = &ArticleSection{
				Title:   title,
				Level:   level,
				Content: "",
			}
		} else if currentSection != nil {
			// Add line to current section content
			if currentSection.Content != "" {
				currentSection.Content += "\n"
			}
			currentSection.Content += line
		}
	}

	// Save last section
	if currentSection != nil {
		currentSection.Content = strings.TrimSpace(currentSection.Content)
		sections = append(sections, *currentSection)
	}

	log.Printf("ExtractSections: Extracted %d sections in %v", len(sections), time.Since(startTime))
	return sections, nil
}

// SuggestNewSection suggests a new section to add to an article
func (c *Client) SuggestNewSection(ctx context.Context, category, subcategory, topic string, existingSections []ArticleSection) (*NewSectionSuggestion, error) {
	startTime := time.Now()

	// Format existing sections
	var sectionsStr string
	for _, s := range existingSections {
		prefix := strings.Repeat("#", s.Level)
		sectionsStr += fmt.Sprintf("%s %s\n", prefix, s.Title)
	}

	// Execute templates
	var systemBuf bytes.Buffer
	if err := c.suggestNewSectionSystemTemplate.Execute(&systemBuf, nil); err != nil {
		return nil, fmt.Errorf("failed to execute suggest_new_section system template: %w", err)
	}

	data := map[string]interface{}{
		"Category":         category,
		"Subcategory":      subcategory,
		"Topic":            topic,
		"ExistingSections": sectionsStr,
	}
	var userBuf bytes.Buffer
	if err := c.suggestNewSectionUserTemplate.Execute(&userBuf, data); err != nil {
		return nil, fmt.Errorf("failed to execute suggest_new_section user template: %w", err)
	}

	resp, err := c.client.CreateChatCompletion(
		ctx,
		openai.ChatCompletionRequest{
			Model: c.modelSummarizeJSON,
			Messages: []openai.ChatCompletionMessage{
				{Role: openai.ChatMessageRoleSystem, Content: systemBuf.String()},
				{Role: openai.ChatMessageRoleUser, Content: userBuf.String()},
			},
			Temperature: 0.5,
		},
	)
	if err != nil {
		return nil, err
	}

	jsonStr := extractJSONObject(resp.Choices[0].Message.Content)
	var result NewSectionSuggestion
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, fmt.Errorf("failed to parse suggest_new_section JSON: %w", err)
	}

	log.Printf("SuggestNewSection: Suggested '%s' in %v", result.SectionTitle, time.Since(startTime))
	return &result, nil
}

// CompareSections compares sections between existing and new articles
func (c *Client) CompareSections(ctx context.Context, topic, existingArticle, existingSections, newArticle, newSections string) (*SectionComparison, error) {
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
	var result SectionComparison
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, fmt.Errorf("failed to parse compare_sections JSON: %w", err)
	}

	log.Printf("CompareSections: Found %d sections to add in %v", len(result.SectionsToAdd), time.Since(startTime))
	return &result, nil
}

// OrderSections determines the optimal order for article sections
func (c *Client) OrderSections(ctx context.Context, req SectionOrderRequest) (*SectionOrderResult, error) {
	startTime := time.Now()

	// Format existing sections as bullet list
	var existingStr string
	for _, s := range req.ExistingSections {
		existingStr += fmt.Sprintf("- %s\n", s.Title)
	}

	// Format new sections as bullet list with reasons
	var newStr string
	for _, s := range req.NewSections {
		if s.Reason != "" {
			newStr += fmt.Sprintf("- %s: %s\n", s.Title, s.Reason)
		} else {
			newStr += fmt.Sprintf("- %s\n", s.Title)
		}
	}

	var systemBuf bytes.Buffer
	if err := c.orderSectionsSystemTemplate.Execute(&systemBuf, nil); err != nil {
		return nil, fmt.Errorf("failed to execute order_sections system template: %w", err)
	}

	data := map[string]interface{}{
		"Topic":            req.Topic,
		"ExistingSections": existingStr,
		"NewSections":      newStr,
	}
	var userBuf bytes.Buffer
	if err := c.orderSectionsUserTemplate.Execute(&userBuf, data); err != nil {
		return nil, fmt.Errorf("failed to execute order_sections user template: %w", err)
	}

	resp, err := c.client.CreateChatCompletion(
		ctx,
		openai.ChatCompletionRequest{
			Model: c.modelSummarizeJSON,
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
	var result SectionOrderResult
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, fmt.Errorf("failed to parse order_sections JSON: %w", err)
	}

	log.Printf("OrderSections: Ordered %d sections in %v", len(result.OrderedTitles), time.Since(startTime))
	return &result, nil
}

// MergeSection merges new content into an existing section
func (c *Client) MergeSection(ctx context.Context, topic, sectionTitle, currentContent, newContent string) (string, error) {
	startTime := time.Now()

	var systemBuf bytes.Buffer
	if err := c.mergeSectionSystemTemplate.Execute(&systemBuf, nil); err != nil {
		return "", fmt.Errorf("failed to execute merge_section system template: %w", err)
	}

	data := map[string]interface{}{
		"Topic":          topic,
		"SectionTitle":   sectionTitle,
		"CurrentContent": currentContent,
		"NewContent":     newContent,
	}
	var userBuf bytes.Buffer
	if err := c.mergeSectionUserTemplate.Execute(&userBuf, data); err != nil {
		return "", fmt.Errorf("failed to execute merge_section user template: %w", err)
	}

	// Use thinking mode for careful merging
	if c.ThinkingEnabled() {
		messages := []ollamaChatMessage{
			{Role: "system", Content: systemBuf.String()},
			{Role: "user", Content: userBuf.String()},
		}
		resp, err := c.chatWithThinking(ctx, c.modelGenerateArticle, messages, 0.3)
		if err != nil {
			return "", err
		}
		log.Printf("MergeSection: Merged '%s' in %v", sectionTitle, time.Since(startTime))
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

	log.Printf("MergeSection: Merged '%s' in %v", sectionTitle, time.Since(startTime))
	return resp.Choices[0].Message.Content, nil
}

// ScoreImprovement scores the quality of an improvement to a section
func (c *Client) ScoreImprovement(ctx context.Context, topic, sectionTitle, originalContent, improvedContent string) (*ImprovementScore, error) {
	startTime := time.Now()

	var systemBuf bytes.Buffer
	if err := c.scoreImprovementSystemTemplate.Execute(&systemBuf, nil); err != nil {
		return nil, fmt.Errorf("failed to execute score_improvement system template: %w", err)
	}

	data := map[string]interface{}{
		"Topic":           topic,
		"SectionTitle":    sectionTitle,
		"OriginalContent": originalContent,
		"ImprovedContent": improvedContent,
	}
	var userBuf bytes.Buffer
	if err := c.scoreImprovementUserTemplate.Execute(&userBuf, data); err != nil {
		return nil, fmt.Errorf("failed to execute score_improvement user template: %w", err)
	}

	resp, err := c.client.CreateChatCompletion(
		ctx,
		openai.ChatCompletionRequest{
			Model: c.modelSummarizeJSON,
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
	var result ImprovementScore
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, fmt.Errorf("failed to parse score_improvement JSON: %w", err)
	}

	log.Printf("ScoreImprovement: Score %d for '%s' in %v", result.Score, sectionTitle, time.Since(startTime))
	return &result, nil
}

// ExtractConcepts identifies valuable concepts from source material that would add value to an article
func (c *Client) ExtractConcepts(ctx context.Context, topic, article, sourceSummary string) (*ConceptExtraction, error) {
	startTime := time.Now()

	var systemBuf bytes.Buffer
	if err := c.extractConceptsSystemTemplate.Execute(&systemBuf, nil); err != nil {
		return nil, fmt.Errorf("failed to execute extract_concepts system template: %w", err)
	}

	data := map[string]interface{}{
		"Topic":         topic,
		"Article":       article,
		"SourceSummary": sourceSummary,
	}
	var userBuf bytes.Buffer
	if err := c.extractConceptsUserTemplate.Execute(&userBuf, data); err != nil {
		return nil, fmt.Errorf("failed to execute extract_concepts user template: %w", err)
	}

	resp, err := c.client.CreateChatCompletion(
		ctx,
		openai.ChatCompletionRequest{
			Model: c.modelSummarizeJSON,
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
	var result ConceptExtraction
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, fmt.Errorf("failed to parse extract_concepts JSON: %w", err)
	}

	log.Printf("ExtractConcepts: Found %d concepts for '%s' in %v", len(result.Concepts), topic, time.Since(startTime))
	return &result, nil
}

// MapConceptToSection determines where a concept should be integrated into an article
func (c *Client) MapConceptToSection(ctx context.Context, topic string, sections []string, concept ExtractedConcept) (*SectionMapping, error) {
	startTime := time.Now()

	var systemBuf bytes.Buffer
	if err := c.mapConceptToSectionSystemTemplate.Execute(&systemBuf, nil); err != nil {
		return nil, fmt.Errorf("failed to execute map_concept_to_section system template: %w", err)
	}

	// Format sections as a numbered list
	var sectionsStr strings.Builder
	for i, s := range sections {
		sectionsStr.WriteString(fmt.Sprintf("%d. %s\n", i+1, s))
	}

	data := map[string]interface{}{
		"Topic":              topic,
		"Sections":           sectionsStr.String(),
		"ConceptName":        concept.Name,
		"ConceptDescription": concept.Description,
	}
	var userBuf bytes.Buffer
	if err := c.mapConceptToSectionUserTemplate.Execute(&userBuf, data); err != nil {
		return nil, fmt.Errorf("failed to execute map_concept_to_section user template: %w", err)
	}

	resp, err := c.client.CreateChatCompletion(
		ctx,
		openai.ChatCompletionRequest{
			Model: c.modelSummarizeJSON,
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
	var result SectionMapping
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, fmt.Errorf("failed to parse map_concept_to_section JSON: %w", err)
	}

	log.Printf("MapConceptToSection: '%s' -> %s '%s' in %v", concept.Name, result.Action, result.TargetSection, time.Since(startTime))
	return &result, nil
}

// RewriteSectionWithConcept rewrites an existing section to naturally incorporate a new concept
func (c *Client) RewriteSectionWithConcept(ctx context.Context, topic, sectionContent string, concept ExtractedConcept) (string, error) {
	startTime := time.Now()

	var systemBuf bytes.Buffer
	if err := c.rewriteSectionWithConceptSystemTemplate.Execute(&systemBuf, nil); err != nil {
		return "", fmt.Errorf("failed to execute rewrite_section_with_concept system template: %w", err)
	}

	data := map[string]interface{}{
		"Topic":          topic,
		"CurrentSection": sectionContent,
		"ConceptName":    concept.Name,
		"ConceptDescription": concept.Description,
		"SourceEvidence":     concept.SourceEvidence,
	}
	var userBuf bytes.Buffer
	if err := c.rewriteSectionWithConceptUserTemplate.Execute(&userBuf, data); err != nil {
		return "", fmt.Errorf("failed to execute rewrite_section_with_concept user template: %w", err)
	}

	resp, err := c.client.CreateChatCompletion(
		ctx,
		openai.ChatCompletionRequest{
			Model: c.modelGenerateArticle,
			Messages: []openai.ChatCompletionMessage{
				{Role: openai.ChatMessageRoleSystem, Content: systemBuf.String()},
				{Role: openai.ChatMessageRoleUser, Content: userBuf.String()},
			},
			Temperature: 0.5,
		},
	)
	if err != nil {
		return "", err
	}

	result := strings.TrimSpace(resp.Choices[0].Message.Content)
	log.Printf("RewriteSectionWithConcept: Rewrote section with '%s' in %v (%d chars)", concept.Name, time.Since(startTime), len(result))
	return result, nil
}

// GenerateNewSection creates a new section for a concept
func (c *Client) GenerateNewSection(ctx context.Context, topic string, concept ExtractedConcept, headingLevel int, existingArticle string) (string, error) {
	startTime := time.Now()

	var systemBuf bytes.Buffer
	if err := c.generateNewSectionSystemTemplate.Execute(&systemBuf, nil); err != nil {
		return "", fmt.Errorf("failed to execute generate_new_section system template: %w", err)
	}

	// Create heading prefix based on level
	headingPrefix := strings.Repeat("#", headingLevel)
	sectionHeading := fmt.Sprintf("%s %s", headingPrefix, concept.Name)

	data := map[string]interface{}{
		"Topic":              topic,
		"SectionHeading":     sectionHeading,
		"HeadingLevel":       headingLevel,
		"ConceptName":        concept.Name,
		"ConceptDescription": concept.Description,
		"SourceEvidence":     concept.SourceEvidence,
		"ExistingArticle":    existingArticle,
	}
	var userBuf bytes.Buffer
	if err := c.generateNewSectionUserTemplate.Execute(&userBuf, data); err != nil {
		return "", fmt.Errorf("failed to execute generate_new_section user template: %w", err)
	}

	resp, err := c.client.CreateChatCompletion(
		ctx,
		openai.ChatCompletionRequest{
			Model: c.modelGenerateArticle,
			Messages: []openai.ChatCompletionMessage{
				{Role: openai.ChatMessageRoleSystem, Content: systemBuf.String()},
				{Role: openai.ChatMessageRoleUser, Content: userBuf.String()},
			},
			Temperature: 0.5,
		},
	)
	if err != nil {
		return "", err
	}

	result := strings.TrimSpace(resp.Choices[0].Message.Content)
	log.Printf("GenerateNewSection: Created '%s' in %v (%d chars)", concept.Name, time.Since(startTime), len(result))
	return result, nil
}
