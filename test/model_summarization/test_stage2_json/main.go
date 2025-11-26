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
		summaryFile = flag.String("summary", "", "Path to plain text summary file from stage 1")
		model       = flag.String("model", "", "Model to use for JSON conversion")
		baseURL     = flag.String("base-url", "http://localhost:11434/v1", "Ollama base URL")
		apiKey      = flag.String("api-key", "ollama", "API key")
		outputDir   = flag.String("output", "test_output", "Output directory for results")
		sourceModel = flag.String("source-model", "", "Model that generated the summary (for labeling)")
	)
	flag.Parse()

	if *summaryFile == "" || *model == "" {
		log.Fatal("Usage: test_stage2_json -summary <summary_file> -model <model> [-source-model <model>]")
	}

	// Read summary file
	content, err := os.ReadFile(*summaryFile)
	if err != nil {
		log.Fatalf("Failed to read summary file: %v", err)
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
	systemPrompt, err := promptsFS.ReadFile("prompts/convert_json_system.txt")
	if err != nil {
		log.Fatalf("Failed to load system prompt: %v", err)
	}

	userPromptTemplate, err := promptsFS.ReadFile("prompts/convert_json_user.txt")
	if err != nil {
		log.Fatalf("Failed to load user prompt: %v", err)
	}

	userPrompt := strings.ReplaceAll(string(userPromptTemplate), "{{.Content}}", string(content))

	srcModel := *sourceModel
	if srcModel == "" {
		// Try to extract from filename
		base := filepath.Base(*summaryFile)
		srcModel = strings.TrimPrefix(strings.TrimSuffix(base, ".txt"), "stage1-")
	}

	fmt.Printf("=== STAGE 2: JSON CONVERSION ===\n")
	fmt.Printf("Converter model: %s\n", *model)
	fmt.Printf("Source summary from: %s\n", srcModel)
	fmt.Printf("Summary length: %d characters\n", len(content))
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
			Temperature: 0.0, // Very low for deterministic JSON output
		},
	)

	duration := time.Since(startTime)

	if err != nil {
		log.Fatalf("LLM request failed: %v", err)
	}

	rawOutput := resp.Choices[0].Message.Content

	// Try to extract and parse JSON
	jsonStr := extractJSONObject(rawOutput)
	var parsed struct {
		Relevant bool   `json:"relevant"`
		Reason   string `json:"reason"`
		Summary  string `json:"summary"`
		Language string `json:"language"`
	}

	jsonValid := true
	parseError := ""
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		jsonValid = false
		parseError = err.Error()
	}

	fmt.Printf("=== RESULTS ===\n")
	fmt.Printf("JSON Valid: %v\n", jsonValid)
	if !jsonValid {
		fmt.Printf("Parse Error: %s\n", parseError)
		fmt.Printf("\nRaw output:\n%s\n", truncate(rawOutput, 500))
	} else {
		fmt.Printf("Relevant: %v\n", parsed.Relevant)
		fmt.Printf("Language: %s\n", parsed.Language)
		fmt.Printf("Summary preserved: %d chars\n", len(parsed.Summary))
	}
	fmt.Printf("Duration: %v\n", duration)

	// Save results
	converterName := sanitizeFilename(*model)
	results := map[string]interface{}{
		"converter_model": *model,
		"source_model":    srcModel,
		"json_valid":      jsonValid,
		"parse_error":     parseError,
		"duration_ms":     duration.Milliseconds(),
		"raw_output":      rawOutput,
	}
	if jsonValid {
		results["relevant"] = parsed.Relevant
		results["reason"] = parsed.Reason
		results["language"] = parsed.Language
		results["summary_length"] = len(parsed.Summary)
	}

	resultsJSON, _ := json.MarshalIndent(results, "", "  ")
	outputPath := filepath.Join(*outputDir, fmt.Sprintf("stage2-%s-from-%s.json", converterName, sanitizeFilename(srcModel)))
	os.WriteFile(outputPath, resultsJSON, 0644)

	fmt.Printf("\nResults saved to: %s\n", outputPath)
}

func extractJSONObject(text string) string {
	start := strings.Index(text, "{")
	if start == -1 {
		return text
	}

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

