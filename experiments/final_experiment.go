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
	} `json:"message"`
}

var prompts = []struct {
	Name   string
	System string
	User   string
}{
	{
		Name: "E1_8b_explicit_length",
		System: `You are a content extractor for an encyclopedia. Extract ALL factual information.

OUTPUT REQUIREMENTS:
- Your output MUST be at least 2000 words
- Cover EVERY topic mentioned in the source
- Use ## for main sections and - for bullet points
- Each section needs 15-25 bullet points
- Include ALL names, dates, numbers, statistics
- DO NOT summarize - EXTRACT everything`,
		User: "Extract all content (minimum 2000 words):\n\n{{CONTENT}}",
	},
	{
		Name: "E2_8b_section_by_section",
		System: `You are extracting content for an encyclopedia article.

PROCESS:
1. First identify ALL sections/topics in the source
2. Then for EACH section, extract 15-20 specific facts
3. Include every name, date, statistic, and detail

FORMAT:
## Section Title
- Fact 1 with specific detail (date, name, number)
- Fact 2 with specific detail
... (continue for 15-20 facts per section)

OUTPUT LENGTH: At least 2500 words covering all sections.`,
		User: "Source document:\n\n{{CONTENT}}",
	},
	{
		Name: "E3_8b_verbatim_extraction",
		System: `Extract content from the source document. Preserve as much original detail as possible.

RULES:
- Extract EVERY fact, statistic, name, and date
- Keep original terminology and phrasing where useful
- Organize by topic with clear headings
- Do NOT condense or summarize
- Include minor details that might seem less important
- Your goal is COMPLETENESS, not brevity

Format: Use ## headings and - bullet points`,
		User: "Document:\n\n{{CONTENT}}",
	},
	{
		Name: "E4_32b_no_think_explicit",
		System: `You are extracting encyclopedic content. Your output must be COMPREHENSIVE.

REQUIREMENTS:
- Minimum 2000 words output
- Cover every major topic from the source
- Include all names, dates, organizations, statistics
- Use ## for sections, - for bullet points
- 15-25 bullet points per section
- Do NOT summarize - EXTRACT fully`,
		User: "Extract everything (at least 2000 words):\n\n{{CONTENT}}",
	},
}

func fetchContent() (string, error) {
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
		chromedp.Navigate("https://en.wikipedia.org/wiki/Climate_change"),
		chromedp.WaitReady("body"),
		chromedp.Sleep(3*time.Second),
		chromedp.OuterHTML("html", &htmlContent),
	)
	if err != nil {
		return "", err
	}

	doc, _ := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	doc.Find("#References, #Notes, #Citations, #External_links, #See_also").Each(func(i int, s *goquery.Selection) {
		s.Parent().NextUntil("h2").Remove()
		s.Parent().Remove()
	})
	doc.Find(".reflist, .references, .navbox, .sidebar, .infobox, .toc, #toc, .mw-editsection").Remove()

	var textBuilder strings.Builder
	doc.Find("#mw-content-text, .mw-parser-output, article, body").First().Find("h1, h2, h3, h4, p, li").Each(func(i int, s *goquery.Selection) {
		if s.ParentsFiltered(".reflist, .references, .navbox, nav, footer").Length() > 0 {
			return
		}
		text := strings.Join(strings.Fields(s.Text()), " ")
		if len(text) > 25 {
			textBuilder.WriteString(text + "\n\n")
		}
	})

	return textBuilder.String(), nil
}

func runLLM(systemPrompt, userPrompt, content, model string, numPredict int) (string, time.Duration, error) {
	userPrompt = strings.ReplaceAll(userPrompt, "{{CONTENT}}", content)

	req := ollamaChatRequest{
		Model: model,
		Messages: []ollamaChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Stream: false,
		Think:  false,
		Options: map[string]interface{}{
			"temperature": 0.3,
			"num_predict": numPredict,
		},
	}

	reqBody, _ := json.Marshal(req)
	start := time.Now()

	httpReq, _ := http.NewRequest("POST", "http://localhost:11434/api/chat", bytes.NewReader(reqBody))
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 20 * time.Minute}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	duration := time.Since(start)

	var chatResp ollamaChatResponse
	if err := json.Unmarshal(body, &chatResp); err != nil {
		return "", duration, err
	}

	return chatResp.Message.Content, duration, nil
}

func main() {
	log.Println("=== Final Optimization Tests ===")

	content, err := fetchContent()
	if err != nil {
		log.Fatalf("Fetch failed: %v", err)
	}
	log.Printf("Got %d chars (%d words)\n", len(content), len(strings.Fields(content)))

	os.MkdirAll("experiments/results", 0755)

	log.Println("\n=== RESULTS ===")
	log.Printf("%-30s %8s %8s %10s\n", "Experiment", "Words", "Chars", "Time")
	log.Println(strings.Repeat("-", 60))

	for _, p := range prompts {
		log.Printf("Running %s...", p.Name)

		model := "qwen3:8b"
		if strings.Contains(p.Name, "32b") {
			model = "qwen3:32b"
		}

		output, duration, err := runLLM(p.System, p.User, content, model, 16000)
		if err != nil {
			log.Printf("  Failed: %v", err)
			continue
		}

		words := len(strings.Fields(output))
		chars := len(output)
		log.Printf("%-30s %8d %8d %10v", p.Name, words, chars, duration.Round(time.Second))

		os.WriteFile(fmt.Sprintf("experiments/results/%s.md", p.Name), []byte(output), 0644)
	}
}

