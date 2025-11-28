package main

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sashabaranov/go-openai"
)

//go:embed prompts/*.txt
var promptsFS embed.FS

func main() {
	var (
		rawFile     = flag.String("raw", "", "Path to raw source file to test")
		topic       = flag.String("topic", "", "Topic for summarization")
		url         = flag.String("url", "", "URL of the source")
		model       = flag.String("model", "", "Model to use for summarization")
		baseURL     = flag.String("base-url", "http://localhost:11434/v1", "Ollama base URL")
		apiKey      = flag.String("api-key", "ollama", "API key")
		outputDir   = flag.String("output", "test_output", "Output directory for results")
	)
	flag.Parse()

	if *rawFile == "" || *topic == "" || *model == "" {
		log.Fatal("Usage: test_summarization -raw <raw_file> -topic <topic> -url <url> -model <model> [-base-url <url>] [-api-key <key>] [-output <dir>]")
	}

	// Read raw file
	content, err := os.ReadFile(*rawFile)
	if err != nil {
		log.Fatalf("Failed to read raw file: %v", err)
	}

	// Create output directory
	if err := os.MkdirAll(*outputDir, 0755); err != nil {
		log.Fatalf("Failed to create output directory: %v", err)
	}

	// Initialize OpenAI client
	config := openai.DefaultConfig(*apiKey)
	config.BaseURL = *baseURL
	client := openai.NewClientWithConfig(config)

	// Load prompts from embedded filesystem
	systemPromptBytes, err := promptsFS.ReadFile("prompts/phase_1_step_1_summarize_source_system.txt")
	if err != nil {
		log.Fatalf("Failed to load system prompt: %v", err)
	}
	systemPrompt := string(systemPromptBytes)

	userPromptTemplateBytes, err := promptsFS.ReadFile("prompts/phase_1_step_1_summarize_source_user.txt")
	if err != nil {
		log.Fatalf("Failed to load user prompt: %v", err)
	}
	userPromptTemplate := string(userPromptTemplateBytes)

	// Replace template variables
	userPrompt := strings.ReplaceAll(userPromptTemplate, "{{.Topic}}", *topic)
	userPrompt = strings.ReplaceAll(userPrompt, "{{.URL}}", *url)
	userPrompt = strings.ReplaceAll(userPrompt, "{{.Content}}", string(content))

	fmt.Printf("Testing model: %s\n", *model)
	fmt.Printf("Topic: %s\n", *topic)
	fmt.Printf("URL: %s\n", *url)
	fmt.Printf("Raw content length: %d characters\n", len(content))
	fmt.Printf("Starting summarization...\n\n")

	startTime := time.Now()

	// Call LLM
	ctx := context.Background()
	resp, err := client.CreateChatCompletion(
		ctx,
		openai.ChatCompletionRequest{
			Model: *model,
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
			Temperature: 0.1,
		},
	)

	duration := time.Since(startTime)

	if err != nil {
		log.Fatalf("LLM request failed: %v", err)
	}

	rawOutput := resp.Choices[0].Message.Content

	// Try to extract JSON
	jsonStr := extractJSONObject(rawOutput)
	
	// Parse JSON
	var summary struct {
		Relevant bool   `json:"relevant"`
		Reason   string `json:"reason"`
		Summary  string `json:"summary"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &summary); err != nil {
		fmt.Printf("WARNING: Failed to parse JSON: %v\n", err)
		fmt.Printf("Raw output:\n%s\n", rawOutput)
		// Save raw output for inspection
		rawPath := filepath.Join(*outputDir, fmt.Sprintf("%s-raw-output.txt", sanitizeFilename(*model)))
		if err := os.WriteFile(rawPath, []byte(rawOutput), 0644); err != nil {
			log.Printf("Failed to save raw output: %v", err)
		}
		return
	}

	// Calculate word count
	wordCount := len(strings.Fields(summary.Summary))

	// Print results
	fmt.Printf("\n=== RESULTS ===\n")
	fmt.Printf("Relevant: %v\n", summary.Relevant)
	fmt.Printf("Reason: %s\n", summary.Reason)
	fmt.Printf("Summary word count: %d\n", wordCount)
	fmt.Printf("Duration: %v\n", duration)
	fmt.Printf("\nSummary preview (first 500 chars):\n%s\n", truncate(summary.Summary, 500))

	// Save results
	modelName := sanitizeFilename(*model)
	results := map[string]interface{}{
		"model":        *model,
		"topic":        *topic,
		"url":          *url,
		"relevant":     summary.Relevant,
		"reason":       summary.Reason,
		"word_count":   wordCount,
		"duration_ms": duration.Milliseconds(),
		"summary":      summary.Summary,
		"raw_output":   rawOutput,
	}

	resultsJSON, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		log.Fatalf("Failed to marshal results: %v", err)
	}

	outputPath := filepath.Join(*outputDir, fmt.Sprintf("%s-results.json", modelName))
	if err := os.WriteFile(outputPath, resultsJSON, 0644); err != nil {
		log.Fatalf("Failed to save results: %v", err)
	}

	fmt.Printf("\nResults saved to: %s\n", outputPath)
}


func extractJSONObject(text string) string {
	// Find first {
	start := strings.Index(text, "{")
	if start == -1 {
		return text
	}

	// Find matching }
	depth := 0
	for i := start; i < len(text); i++ {
		if text[i] == '{' {
			depth++
		} else if text[i] == '}' {
			depth--
			if depth == 0 {
				return text[start : i+1]
			}
		}
	}

	return text[start:]
}

func sanitizeFilename(name string) string {
	return strings.ReplaceAll(strings.ReplaceAll(name, ":", "-"), "/", "-")
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

