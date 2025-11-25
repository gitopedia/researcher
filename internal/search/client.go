package search

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/chromedp/chromedp"
)

type Client struct {
	httpClient *http.Client
	apiBaseURL string
}

type Result struct {
	Title string
	Href  string
	Body  string
}

func NewClient() *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 10 * time.Second},
		apiBaseURL: "https://api.duckduckgo.com/",
	}
}

// Search performs a DuckDuckGo search via HTML scraping
func (c *Client) Search(query string) ([]Result, error) {
	fmt.Printf("Searching for: %s\n", query)

	// Sleep slightly to be polite
	time.Sleep(1 * time.Second)

	u := fmt.Sprintf("https://html.duckduckgo.com/html/?q=%s", url.QueryEscape(query))
	req, _ := http.NewRequest("GET", u, nil)
	// Use a generic User-Agent to avoid being blocked immediately
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bad status: %s", resp.Status)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, err
	}

	var results []Result
	doc.Find(".result").Each(func(i int, s *goquery.Selection) {
		if len(results) >= 5 {
			return
		}
		link := s.Find(".result__title .result__a")
		title := strings.TrimSpace(link.Text())
		href, _ := link.Attr("href")
		snippet := strings.TrimSpace(s.Find(".result__snippet").Text())

		if href != "" && title != "" {
			// Handle DDG redirects if necessary (usually /l/?kh=...)
			// We'll just keep the link as is for now; FetchContent will follow redirects if it's a valid URL.
			// If it's relative, we prepend host.
			if strings.HasPrefix(href, "/") {
				href = "https://html.duckduckgo.com" + href
			}
			results = append(results, Result{Title: title, Href: href, Body: snippet})
		}
	})

	return results, nil
}

func (c *Client) FetchContent(targetURL string) (string, error) {
	fmt.Printf("Fetching content (headless): %s\n", targetURL)

	// Create allocator options
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),              // Required for Docker/root
		chromedp.Flag("disable-dev-shm-usage", true),   // Prevent shared memory issues in Docker
		chromedp.Flag("ignore-certificate-errors", true),
	)

	// Create allocator context
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancelAlloc()

	// Create context
	ctx, cancelCtx := chromedp.NewContext(allocCtx)
	defer cancelCtx()

	// Set a timeout for the entire operation
	ctx, cancelTimeout := context.WithTimeout(ctx, 45*time.Second)
	defer cancelTimeout()

	var htmlContent string
	// Navigate and wait for content
	err := chromedp.Run(ctx,
		chromedp.Navigate(targetURL),
		chromedp.WaitReady("body"),
		chromedp.Sleep(2*time.Second),
		chromedp.OuterHTML("html", &htmlContent),
	)
	if err != nil {
		return "", fmt.Errorf("headless fetch failed: %w", err)
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		return "", err
	}

	// Extract readable text (paragraphs and headers)
	var textBuilder strings.Builder
	// Expanded selector to capture more structure
	doc.Find("h1, h2, h3, p, li").Each(func(i int, s *goquery.Selection) {
		// Normalize whitespace
		text := strings.Join(strings.Fields(s.Text()), " ")
		if len(text) > 20 { // Filter out very short snippets/nav items
			textBuilder.WriteString(text + "\n\n")
		}
	})

	text := textBuilder.String()
	// Limit to 10000 chars to provide more context to LLM
	if len(text) > 10000 {
		text = text[:10000] + "..."
	}
	return strings.TrimSpace(text), nil
}
