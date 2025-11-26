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
		rawFile   = flag.String("raw", "", "Path to raw source file to test")
		topic     = flag.String("topic", "", "Topic for summarization")
		url       = flag.String("url", "", "URL of the source")
		model     = flag.String("model", "", "Model to use for summarization")
		baseURL   = flag.String("base-url", "http://localhost:11434/v1", "Ollama base URL")
		apiKey    = flag.String("api-key", "ollama", "API key")
		outputDir = flag.String("output", "test_output", "Output directory for results")
	)
	flag.Parse()

	if *rawFile == "" || *topic == "" || *model == "" {
		log.Fatal("Usage: test_stage1_summarize -raw <raw_file> -topic <topic> -url <url> -model <model>")
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

	// Load prompts
	systemPrompt, err := promptsFS.ReadFile("prompts/summarize_plaintext_system.txt")
	if err != nil {
		log.Fatalf("Failed to load system prompt: %v", err)
	}

	userPromptTemplate, err := promptsFS.ReadFile("prompts/summarize_plaintext_user.txt")
	if err != nil {
		log.Fatalf("Failed to load user prompt: %v", err)
	}

	// Replace template variables
	userPrompt := strings.ReplaceAll(string(userPromptTemplate), "{{.Topic}}", *topic)
	userPrompt = strings.ReplaceAll(userPrompt, "{{.URL}}", *url)
	userPrompt = strings.ReplaceAll(userPrompt, "{{.Content}}", string(content))

	fmt.Printf("=== STAGE 1: SUMMARIZATION ===\n")
	fmt.Printf("Model: %s\n", *model)
	fmt.Printf("Topic: %s\n", *topic)
	fmt.Printf("Raw content length: %d characters\n", len(content))
	fmt.Printf("Starting...\n\n")

	startTime := time.Now()

	// Call LLM
	ctx := context.Background()
	resp, err := client.CreateChatCompletion(
		ctx,
		openai.ChatCompletionRequest{
			Model: *model,
			Messages: []openai.ChatCompletionMessage{
				{Role: openai.ChatMessageRoleSystem, Content: string(systemPrompt)},
				{Role: openai.ChatMessageRoleUser, Content: userPrompt},
			},
			Temperature: 0.3,
		},
	)

	duration := time.Since(startTime)

	if err != nil {
		log.Fatalf("LLM request failed: %v", err)
	}

	output := resp.Choices[0].Message.Content
	wordCount := len(strings.Fields(output))

	// Check if content was marked as not relevant
	isRelevant := !strings.HasPrefix(strings.TrimSpace(output), "NOT_RELEVANT")

	fmt.Printf("=== RESULTS ===\n")
	fmt.Printf("Relevant: %v\n", isRelevant)
	fmt.Printf("Word count: %d\n", wordCount)
	fmt.Printf("Duration: %v\n", duration)
	fmt.Printf("\nPreview (first 500 chars):\n%s\n", truncate(output, 500))

	// Save results
	modelName := sanitizeFilename(*model)
	results := map[string]interface{}{
		"model":       *model,
		"topic":       *topic,
		"url":         *url,
		"relevant":    isRelevant,
		"word_count":  wordCount,
		"duration_ms": duration.Milliseconds(),
		"summary":     output,
	}

	resultsJSON, _ := json.MarshalIndent(results, "", "  ")
	outputPath := filepath.Join(*outputDir, fmt.Sprintf("stage1-%s.json", modelName))
	os.WriteFile(outputPath, resultsJSON, 0644)

	// Also save plain text for stage 2
	textPath := filepath.Join(*outputDir, fmt.Sprintf("stage1-%s.txt", modelName))
	os.WriteFile(textPath, []byte(output), 0644)

	fmt.Printf("\nResults saved to: %s\n", outputPath)
	fmt.Printf("Plain text saved to: %s\n", textPath)
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

