package main

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
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/chromedp/chromedp"
)

type ollamaChatRequest struct {
	Model    string                 `json:"model"`
	Messages []ollamaChatMessage    `json:"messages"`
	Stream   bool                   `json:"stream"`
	Think    interface{}            `json:"think,omitempty"`
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

var experiments = []struct {
	Name        string
	SystemPrompt string
	UserPrompt  string
	Think       interface{}
	Temperature float64
	NumPredict  int
}{
	{
		Name: "1_baseline_no_thinking",
		SystemPrompt: `You extract informative content from web pages for encyclopedia articles.
Extract ALL useful factual content. Use heading + bullet format.
# Heading
- Bullet point with fact
Target: 3000-6000 words. Do NOT compress.`,
		UserPrompt: "Extract content from this page about {{TOPIC}}:\n\n{{CONTENT}}",
		Think:      false,
		Temperature: 0.3,
		NumPredict: 16000,
	},
	{
		Name: "2_explicit_sections",
		SystemPrompt: `You are extracting encyclopedia content. For the topic provided, you MUST cover ALL of these sections if present in the source:

1. Definition and Overview
2. Historical Background/Timeline
3. Scientific Basis/Mechanism
4. Key People and Contributors
5. Major Events and Milestones
6. Current Status/Recent Developments
7. Impacts and Effects
8. Controversies and Debates
9. Future Outlook
10. Related Topics

For EACH section, provide 10-20 bullet points of specific facts.
Use format:
# Section Name
- Specific fact with date, name, or number
- Another specific fact`,
		UserPrompt: "Extract ALL content about {{TOPIC}} from this source, covering every section listed:\n\n{{CONTENT}}",
		Think:      false,
		Temperature: 0.3,
		NumPredict: 16000,
	},
	{
		Name: "3_negative_instructions",
		SystemPrompt: `You are a content extractor. Your output must be VERY LONG and DETAILED.

CRITICAL RULES:
- DO NOT summarize
- DO NOT condense
- DO NOT compress
- DO NOT shorten
- DO NOT skip details
- DO NOT paraphrase (use original phrasing when possible)

Instead:
- EXTRACT everything
- PRESERVE all details
- INCLUDE every fact
- COPY all statistics and numbers
- LIST every person mentioned
- RECORD every date

Output format: Headings with # followed by bullet points with -
Minimum output: 5000 words`,
		UserPrompt: "EXTRACT (do not summarize) all content from this page:\n\n{{CONTENT}}",
		Think:      false,
		Temperature: 0.5,
		NumPredict: 16000,
	},
	{
		Name: "4_chunked_approach",
		SystemPrompt: `Extract ALL content from this text chunk. Do not summarize - preserve details.
Output as bullet points under topic headings.
Include: all names, dates, numbers, statistics, organizations, events.`,
		UserPrompt: "Extract content chunk:\n\n{{CONTENT}}",
		Think:      false,
		Temperature: 0.3,
		NumPredict: 8000,
	},
	{
		Name: "5_roleplay_researcher",
		SystemPrompt: `You are a meticulous research assistant at an encyclopedia publisher. Your job is to extract EVERY piece of factual information from source documents for writers to use.

Your extraction will be reviewed by senior editors who expect COMPREHENSIVE coverage. Missing information is unacceptable.

For each topic/section in the source:
1. List the heading
2. Extract EVERY fact, statistic, name, date, and detail
3. Preserve technical terminology
4. Note relationships between concepts

Quality metrics your extraction will be judged on:
- Completeness (did you get everything?)
- Accuracy (did you preserve details correctly?)
- Organization (is it well-structured?)

Output at least 4000 words for a comprehensive source.`,
		UserPrompt: "Source document for encyclopedia article on {{TOPIC}}:\n\n{{CONTENT}}\n\n---\nBegin your comprehensive extraction:",
		Think:      "medium",
		Temperature: 0.3,
		NumPredict: 16000,
	},
	{
		Name: "6_json_structured",
		SystemPrompt: `Extract content into a structured JSON format. For each major section:
{
  "sections": [
    {
      "title": "Section Name",
      "facts": [
        "Fact 1 with specific detail",
        "Fact 2 with date/name/number",
        ...at least 10 facts per section
      ]
    }
  ]
}
Include ALL sections from the source. Each section needs 10-20 facts minimum.`,
		UserPrompt: "Extract into JSON:\n\n{{CONTENT}}",
		Think:      false,
		Temperature: 0.1,
		NumPredict: 16000,
	},
	{
		Name: "7_two_pass_outline_first",
		SystemPrompt: `STEP 1: First, list all the major topics/sections in this document.
STEP 2: Then, for EACH topic, extract 10-20 specific facts.

Format:
## OUTLINE
1. Topic A
2. Topic B
...

## EXTRACTED CONTENT
# Topic A
- Fact 1
- Fact 2
...
# Topic B
- Fact 1
...`,
		UserPrompt: "Document to extract:\n\n{{CONTENT}}",
		Think:      false,
		Temperature: 0.3,
		NumPredict: 16000,
	},
	{
		Name: "8_high_temp_verbose",
		SystemPrompt: `Extract all content from this source document. Be extremely thorough and verbose.
Include every detail, statistic, name, date, and fact.
Use bullet points under headings.
Your output should be as long as necessary to capture everything.`,
		UserPrompt: "Extract all content:\n\n{{CONTENT}}",
		Think:      false,
		Temperature: 0.8,
		NumPredict: 16000,
	},
	{
		Name: "9_smaller_model_8b",
		SystemPrompt: `Extract all factual content from this web page. Format as:
# Heading
- Bullet point
Include all facts, dates, names, statistics. Be thorough.`,
		UserPrompt: "Page content:\n\n{{CONTENT}}",
		Think:      false,
		Temperature: 0.3,
		NumPredict: 16000,
	},
	{
		Name: "10_paragraph_format",
		SystemPrompt: `Extract all content from this source into well-organized paragraphs (not bullet points).
Write in full sentences, preserving all details.
Organize by topic with clear headings.
Be comprehensive - include every fact, statistic, name, and date.
Target: at least 3000 words.`,
		UserPrompt: "Source document:\n\n{{CONTENT}}",
		Think:      false,
		Temperature: 0.4,
		NumPredict: 16000,
	},
}

func fetchWikipediaContent(url string) (string, error) {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
	)

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancelAlloc()

	ctx, cancelCtx := chromedp.NewContext(allocCtx)
	defer cancelCtx()

	ctx, cancelTimeout := context.WithTimeout(ctx, 60*time.Second)
	defer cancelTimeout()

	var htmlContent string
	err := chromedp.Run(ctx,
		chromedp.Navigate(url),
		chromedp.WaitReady("body"),
		chromedp.Sleep(3*time.Second),
		chromedp.OuterHTML("html", &htmlContent),
	)
	if err != nil {
		return "", err
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		return "", err
	}

	// Remove reference sections
	doc.Find("#References, #Notes, #Citations, #External_links, #See_also, #Further_reading").Each(func(i int, s *goquery.Selection) {
		s.Parent().NextUntil("h2").Remove()
		s.Parent().Remove()
	})
	doc.Find(".reflist, .references, .citation, .navbox, .sidebar, .infobox, .toc, #toc").Remove()

	var textBuilder strings.Builder
	
	// Try multiple selectors
	selectors := []string{".mw-parser-output", "#mw-content-text", "#bodyContent", "article", "main"}
	var found bool
	for _, sel := range selectors {
		container := doc.Find(sel).First()
		if container.Length() > 0 {
			container.Find("h1, h2, h3, h4, p, li").Each(func(i int, s *goquery.Selection) {
				if s.ParentsFiltered(".reflist, .references, .navbox, .sidebar, nav, footer, .toc, .mw-editsection").Length() > 0 {
					return
				}
				text := strings.Join(strings.Fields(s.Text()), " ")
				if len(text) > 20 {
					textBuilder.WriteString(text + "\n\n")
				}
			})
			if textBuilder.Len() > 0 {
				found = true
				break
			}
		}
	}
	
	// Fallback to body
	if !found {
		doc.Find("body").Find("h1, h2, h3, h4, p, li").Each(func(i int, s *goquery.Selection) {
			text := strings.Join(strings.Fields(s.Text()), " ")
			if len(text) > 20 {
				textBuilder.WriteString(text + "\n\n")
			}
		})
	}

	return textBuilder.String(), nil
}

func runExperiment(name, systemPrompt, userPrompt, content, topic string, think interface{}, temp float64, numPredict int, model string) (string, time.Duration, error) {
	userPrompt = strings.ReplaceAll(userPrompt, "{{CONTENT}}", content)
	userPrompt = strings.ReplaceAll(userPrompt, "{{TOPIC}}", topic)

	options := map[string]interface{}{
		"temperature": temp,
	}
	if numPredict > 0 {
		options["num_predict"] = numPredict
	}

	req := ollamaChatRequest{
		Model: model,
		Messages: []ollamaChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Stream:  false,
		Think:   think,
		Options: options,
	}

	reqBody, err := json.Marshal(req)
	if err != nil {
		return "", 0, err
	}

	start := time.Now()
	httpReq, _ := http.NewRequest("POST", "http://localhost:11434/api/chat", bytes.NewReader(reqBody))
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Minute}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", 0, err
	}
	duration := time.Since(start)

	var chatResp ollamaChatResponse
	if err := json.Unmarshal(body, &chatResp); err != nil {
		return "", duration, fmt.Errorf("parse error: %w, body: %s", err, string(body)[:min(500, len(body))])
	}

	return chatResp.Message.Content, duration, nil
}

func countWords(s string) int {
	return len(strings.Fields(s))
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func main() {
	log.Println("=== Summarization Experiment Suite ===")
	log.Println("Fetching Wikipedia content...")

	content, err := fetchWikipediaContent("https://en.wikipedia.org/wiki/Climate_change")
	if err != nil {
		log.Fatalf("Failed to fetch content: %v", err)
	}
	log.Printf("Fetched %d chars (%d words) of content\n", len(content), countWords(content))

	// Truncate for chunked experiment
	chunkSize := len(content) / 3
	chunks := []string{
		content[:chunkSize],
		content[chunkSize : 2*chunkSize],
		content[2*chunkSize:],
	}

	os.MkdirAll("experiments/results", 0755)

	results := []struct {
		Name     string
		Words    int
		Chars    int
		Duration time.Duration
	}{}

	for _, exp := range experiments {
		log.Printf("\n--- Running: %s ---\n", exp.Name)

		model := "qwen3:32b"
		if exp.Name == "9_smaller_model_8b" {
			model = "qwen3:8b"
		}

		var output string
		var duration time.Duration

		if exp.Name == "4_chunked_approach" {
			// Run on each chunk and combine
			var combined strings.Builder
			var totalDuration time.Duration
			for i, chunk := range chunks {
				log.Printf("  Processing chunk %d/3...", i+1)
				out, dur, err := runExperiment(exp.Name, exp.SystemPrompt, exp.UserPrompt, chunk, "Climate Change", exp.Think, exp.Temperature, exp.NumPredict, model)
				if err != nil {
					log.Printf("  Chunk %d failed: %v", i+1, err)
					continue
				}
				combined.WriteString(fmt.Sprintf("\n## CHUNK %d\n%s\n", i+1, out))
				totalDuration += dur
			}
			output = combined.String()
			duration = totalDuration
		} else {
			var err error
			output, duration, err = runExperiment(exp.Name, exp.SystemPrompt, exp.UserPrompt, content, "Climate Change", exp.Think, exp.Temperature, exp.NumPredict, model)
			if err != nil {
				log.Printf("  Failed: %v", err)
				continue
			}
		}

		words := countWords(output)
		chars := len(output)
		log.Printf("  Result: %d words, %d chars, took %v", words, chars, duration)

		// Save output
		filename := fmt.Sprintf("experiments/results/%s.md", exp.Name)
		os.WriteFile(filename, []byte(output), 0644)
		log.Printf("  Saved to %s", filename)

		results = append(results, struct {
			Name     string
			Words    int
			Chars    int
			Duration time.Duration
		}{exp.Name, words, chars, duration})
	}

	// Summary
	log.Println("\n=== RESULTS SUMMARY ===")
	log.Printf("%-35s %8s %8s %10s\n", "Experiment", "Words", "Chars", "Time")
	log.Println(strings.Repeat("-", 70))
	for _, r := range results {
		log.Printf("%-35s %8d %8d %10v\n", r.Name, r.Words, r.Chars, r.Duration.Round(time.Second))
	}
}

