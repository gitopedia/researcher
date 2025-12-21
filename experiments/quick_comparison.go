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

const systemPrompt = `You extract informative content from web pages for encyclopedia articles.

Your task is to extract ALL useful, factual content and produce a COMPREHENSIVE extraction.

**WHAT TO EXTRACT:**
- ALL factual, informative content
- Key facts, dates, historical context, definitions, statistics
- Notable people, events, places, organizations
- Technical details, scientific information
- Specific numbers, percentages, measurements

**OUTPUT FORMAT:**
Use heading + bullet-list structure:
# Section Name
- Fact with specific detail
- Another fact with date/name/number

Cover ALL major sections from the source. Each section should have 10-20 bullet points.
Do NOT summarize - EXTRACT and preserve information.`

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

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		return "", err
	}

	// Remove junk
	doc.Find("#References, #Notes, #Citations, #External_links, #See_also").Each(func(i int, s *goquery.Selection) {
		s.Parent().NextUntil("h2").Remove()
		s.Parent().Remove()
	})
	doc.Find(".reflist, .references, .navbox, .sidebar, .infobox, .toc, #toc, .mw-editsection").Remove()

	var textBuilder strings.Builder
	doc.Find("#mw-content-text, .mw-parser-output, #bodyContent, article, main, body").First().Find("h1, h2, h3, h4, p, li").Each(func(i int, s *goquery.Selection) {
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

func runLLM(content string, think interface{}, model string, numPredict int) (string, time.Duration, error) {
	userPrompt := fmt.Sprintf("Extract ALL content from this page about Climate Change:\n\n%s", content)

	options := map[string]interface{}{
		"temperature": 0.3,
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
		return "", duration, fmt.Errorf("parse error: %w", err)
	}

	return chatResp.Message.Content, duration, nil
}

func countWords(s string) int {
	return len(strings.Fields(s))
}

func main() {
	log.Println("=== Quick Comparison: Thinking vs No-Thinking ===")

	// Fetch content
	log.Println("Fetching Wikipedia content...")
	content, err := fetchContent("https://en.wikipedia.org/wiki/Climate_change")
	if err != nil {
		log.Fatalf("Fetch failed: %v", err)
	}
	log.Printf("Got %d chars (%d words)\n", len(content), countWords(content))

	if len(content) < 1000 {
		log.Println("Content too short, using fallback...")
		content = `Climate change refers to long-term shifts in global temperatures and weather patterns. Human activities have been the main driver of climate change since the 1800s, primarily due to burning fossil fuels like coal, oil and gas.

The greenhouse effect is a natural process that warms the Earth's surface. When the Sun's energy reaches the Earth's atmosphere, some of it is reflected back to space and the rest is absorbed and re-radiated by greenhouse gases. Greenhouse gases include water vapour, carbon dioxide, methane, nitrous oxide, ozone and some artificial chemicals.

Climate change has many effects including rising sea levels, more frequent extreme weather events, changes in precipitation patterns, and impacts on ecosystems and biodiversity. The Intergovernmental Panel on Climate Change (IPCC) has documented these changes extensively.

Key historical figures in climate science include Joseph Fourier who proposed the greenhouse effect in the 1820s, John Tyndall who identified greenhouse gases in 1859, Svante Arrhenius who calculated warming from CO2 in 1896, and Charles Keeling who began systematic CO2 measurements in 1958.

The Paris Agreement of 2015 aims to limit global warming to well below 2°C above pre-industrial levels. Many countries have committed to reaching net-zero emissions by 2050.`
	}

	os.MkdirAll("experiments/results", 0755)

	experiments := []struct {
		Name   string
		Think  interface{}
		Model  string
	}{
		{"A_32b_no_thinking", false, "qwen3:32b"},
		{"B_32b_thinking_true", true, "qwen3:32b"},
		{"C_32b_thinking_low", "low", "qwen3:32b"},
		{"D_8b_no_thinking", false, "qwen3:8b"},
	}

	results := make([]struct {
		Name     string
		Words    int
		Chars    int
		Duration time.Duration
	}, 0)

	for _, exp := range experiments {
		log.Printf("\n--- %s ---\n", exp.Name)

		output, duration, err := runLLM(content, exp.Think, exp.Model, 16000)
		if err != nil {
			log.Printf("Failed: %v", err)
			continue
		}

		words := countWords(output)
		chars := len(output)
		log.Printf("Result: %d words, %d chars, %v", words, chars, duration.Round(time.Second))

		os.WriteFile(fmt.Sprintf("experiments/results/%s.md", exp.Name), []byte(output), 0644)

		results = append(results, struct {
			Name     string
			Words    int
			Chars    int
			Duration time.Duration
		}{exp.Name, words, chars, duration})
	}

	log.Println("\n=== SUMMARY ===")
	log.Printf("%-25s %8s %8s %10s\n", "Experiment", "Words", "Chars", "Time")
	log.Println(strings.Repeat("-", 55))
	for _, r := range results {
		log.Printf("%-25s %8d %8d %10v\n", r.Name, r.Words, r.Chars, r.Duration.Round(time.Second))
	}
}

