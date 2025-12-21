package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
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
		Content string `json:"content"`
	} `json:"message"`
}

const systemPrompt = `You extract informative content from web pages for encyclopedia articles.

Your task is to EXTRACT (not summarize) all useful factual content from the source.

OUTPUT FORMAT:
Use the same section structure as the source document. For each section:
# Section Heading
- Specific fact with concrete detail
- Another fact (include any names, dates, numbers, places)
- Continue for all facts in that section

EXTRACTION RULES:
- Follow the source's own organization - use its headings and structure
- Include ALL facts: names, dates, numbers, statistics, places, events
- Preserve specific details - do not generalize or round numbers
- Each bullet should be a complete, informative statement
- Cover every section present in the source

LENGTH REQUIREMENT:
Your output should be proportional to the source length. For comprehensive sources, output 1500-3000 words. Do not artificially compress.

OUTPUT ONLY the extracted content. Do not add:
- Introductions or conclusions you invented
- Conversational phrases ("Let me know...", "I hope this helps...")
- Meta-commentary about the extraction process`

func fetchContent(url string) (string, error) {
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

func runLLM(topic, content string) (string, time.Duration, error) {
	userPrompt := fmt.Sprintf(`Extract all factual content about "%s" from this web page.

Output "NOT_RELEVANT" only if the page has no useful content.

Otherwise, extract comprehensively using heading + bullet format.

Page Content:
%s`, topic, content)

	req := ollamaChatRequest{
		Model: "qwen3:32b",
		Messages: []ollamaChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Stream: false,
		Think:  false,
		Options: map[string]interface{}{
			"temperature": 0.3,
			"num_predict": 16000,
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
	topics := []struct {
		Name string
		URL  string
	}{
		{"Jazz", "https://en.wikipedia.org/wiki/Jazz"},
		{"Ancient Rome", "https://en.wikipedia.org/wiki/Ancient_Rome"},
		{"Photosynthesis", "https://en.wikipedia.org/wiki/Photosynthesis"},
	}

	log.Println("=== Multi-Topic Generalization Test ===")
	log.Printf("%-20s %8s %8s %10s\n", "Topic", "Words", "Chars", "Time")
	log.Println(strings.Repeat("-", 50))

	for _, t := range topics {
		log.Printf("Fetching %s...", t.Name)
		content, err := fetchContent(t.URL)
		if err != nil {
			log.Printf("  Fetch failed: %v", err)
			continue
		}
		log.Printf("  Got %d chars", len(content))

		log.Printf("  Running LLM...")
		output, duration, err := runLLM(t.Name, content)
		if err != nil {
			log.Printf("  LLM failed: %v", err)
			continue
		}

		words := len(strings.Fields(output))
		chars := len(output)
		log.Printf("%-20s %8d %8d %10v", t.Name, words, chars, duration.Round(time.Second))

		// Show first 500 chars
		preview := output
		if len(preview) > 800 {
			preview = preview[:800] + "..."
		}
		log.Printf("  Preview:\n%s\n", preview)
	}
}

