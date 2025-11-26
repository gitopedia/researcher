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
	baseUrl                          string
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
}

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

	// Global model configuration
	// LLM_MODEL_FAST: smaller/faster model (JSON conversion, utility tasks)
	// LLM_MODEL_DETAILED: larger/more capable model (article & summarization)
	fastModel := os.Getenv("LLM_MODEL_FAST")
	detailedModel := os.Getenv("LLM_MODEL_DETAILED")

	// Backwards compatibility: fall back to legacy OPENAI_MODEL if present
	if fastModel == "" && detailedModel == "" {
		if legacy := os.Getenv("OPENAI_MODEL"); legacy != "" {
			fastModel = legacy
		}
	}
	if fastModel == "" {
		fastModel = "gemma3:12b"
	}
	if detailedModel == "" {
		detailedModel = fastModel
	}

	// Per-task model configuration (with fallback to fast/detailed)
	modelGenerateArticle := getEnvOrDefault("LLM_MODEL_GENERATE_ARTICLE", detailedModel)
	modelExtractEntities := getEnvOrDefault("LLM_MODEL_EXTRACT_ENTITIES", fastModel)
	modelSuggestTopics := getEnvOrDefault("LLM_MODEL_SUGGEST_TOPICS", fastModel)
	modelSummarizePlain := getEnvOrDefault("LLM_MODEL_SUMMARIZE_PLAIN", detailedModel)
	modelSummarizeJSON := getEnvOrDefault("LLM_MODEL_SUMMARIZE_JSON", fastModel)

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

	summarizeSourceSystem, err := loadTemplate("prompts/summarize_source_system.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load summarize_source_system template: %w", err)
	}

	summarizeSourceUser, err := loadTemplate("prompts/summarize_source_user.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load summarize_source_user template: %w", err)
	}

	convertSummarySystem, err := loadTemplate("prompts/convert_summary_system.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load convert_summary_system template: %w", err)
	}

	convertSummaryUser, err := loadTemplate("prompts/convert_summary_user.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load convert_summary_user template: %w", err)
	}

	return &Client{
		client:                        openai.NewClientWithConfig(config),
		baseUrl:                       baseUrl,
		modelGenerateArticle:          modelGenerateArticle,
		modelExtractEntities:          modelExtractEntities,
		modelSuggestTopics:            modelSuggestTopics,
		modelSummarizePlain:           modelSummarizePlain,
		modelSummarizeJSON:           modelSummarizeJSON,
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
	}, nil
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
			Model: c.modelExtractEntities,
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

	jsonStr := extractJSONArray(resp.Choices[0].Message.Content)

	var topics []string
	if err := json.Unmarshal([]byte(jsonStr), &topics); err != nil {
		return nil, fmt.Errorf("failed to parse topics JSON: %w (input: %q)", err, jsonStr)
	}

	return topics, nil
}

// SummarizeSource compresses a single source page into a focused summary and
// decides whether it is relevant enough to include.
// It now uses a two-step process:
//  1) Plain-text summarization with structured headings/bullets.
//  2) JSON conversion (relevance + reason + language + topics) without changing the text.
func (c *Client) SummarizeSource(ctx context.Context, topic, urlStr, content string) (SourceSummary, error) {
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

	resp, err := c.client.CreateChatCompletion(
		ctx,
		openai.ChatCompletionRequest{
			Model: c.modelSummarizePlain,
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
			Temperature: 0.3, // allow some variation while remaining stable
		},
	)
	if err != nil {
		return SourceSummary{}, err
	}

	plain := strings.TrimSpace(resp.Choices[0].Message.Content)

	summary := SourceSummary{
		Model:    c.modelSummarizePlain,
		Language: detectLanguage(content),
		Raw:      plain,
	}

	// If the model decides the page is not relevant, it should output NOT_RELEVANT.
	upper := strings.ToUpper(strings.TrimSpace(plain))
	if strings.HasPrefix(upper, "NOT_RELEVANT") {
		summary.Relevant = false
		summary.Reason = "Marked NOT_RELEVANT by summarization model"
		summary.Summary = ""
		return summary, nil
	}

	// Stage 2: JSON conversion (relevance + reason + topics), using a fast model.
	var convSystemBuf bytes.Buffer
	if err := c.convertSummarySystemTemplate.Execute(&convSystemBuf, nil); err != nil {
		// Fallback: return the plain summary marked as relevant.
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
			Temperature: 0.0, // deterministic JSON
		},
	)
	if err != nil {
		// Fallback: return plain summary without JSON enrichment.
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
		// Fallback: keep plain summary but surface the error for logging.
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
