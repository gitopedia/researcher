package search

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/chromedp/chromedp"
)

type Client struct {
	httpClient      *http.Client
	apiBaseURL      string
	maxChars        int
	maxResultsPerQuery int
}

type Result struct {
	Title string
	Href  string
	Body  string
}

func NewClient() *Client {
	// Default max chars; can be overridden via SEARCH_MAX_CHARS.
	maxChars := 128000
	if envVal := os.Getenv("SEARCH_MAX_CHARS"); envVal != "" {
		if v, err := strconv.Atoi(envVal); err == nil && v > 0 {
			maxChars = v
		}
	}

	maxResultsPerQuery := 10
	if envVal := os.Getenv("SEARCH_RESULTS_PER_QUERY"); envVal != "" {
		if v, err := strconv.Atoi(envVal); err == nil && v > 0 {
			maxResultsPerQuery = v
		}
	}

	return &Client{
		httpClient:      &http.Client{Timeout: 10 * time.Second},
		apiBaseURL:      "https://api.duckduckgo.com/",
		maxChars:        maxChars,
		maxResultsPerQuery: maxResultsPerQuery,
	}
}

// Search performs a DuckDuckGo search via HTML scraping
func (c *Client) Search(query string) ([]Result, error) {
	fmt.Printf("Searching for: %s\n", query)

	// Sleep slightly to be polite
	time.Sleep(2 * time.Second)

	u := fmt.Sprintf("https://html.duckduckgo.com/html/?q=%s", url.QueryEscape(query))
	
	// Retry logic for rate limiting
	maxRetries := 3
	var resp *http.Response
	var err error
	
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff: 2s, 4s, 8s
			backoff := time.Duration(1<<uint(attempt)) * 2 * time.Second
			log.Printf("Retrying search (attempt %d/%d) after %v...", attempt+1, maxRetries, backoff)
			time.Sleep(backoff)
		}
		
		req, _ := http.NewRequest("GET", u, nil)
		// Use a generic User-Agent to avoid being blocked immediately
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
		
		resp, err = c.httpClient.Do(req)
		if err != nil {
			if attempt < maxRetries-1 {
				continue
			}
			return nil, err
		}
		defer resp.Body.Close()

		// 202 Accepted might mean rate limiting - retry
		if resp.StatusCode == http.StatusAccepted {
			if attempt < maxRetries-1 {
				continue
			}
			return nil, fmt.Errorf("bad status: %s (rate limited?)", resp.Status)
		}
		
		if resp.StatusCode == http.StatusOK {
			break
		}
		
		// Other non-200 status - retry once more
		if attempt < maxRetries-1 {
			continue
		}
		return nil, fmt.Errorf("bad status: %s", resp.Status)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, err
	}

	var results []Result
	maxResults := c.maxResultsPerQuery
	if maxResults <= 0 {
		maxResults = 50 // Default to 50 to get all results from first page
	}
	doc.Find(".result").Each(func(i int, s *goquery.Selection) {
		if len(results) >= maxResults {
			return
		}
		link := s.Find(".result__title .result__a")
		title := strings.TrimSpace(link.Text())
		href, _ := link.Attr("href")
		snippet := strings.TrimSpace(s.Find(".result__snippet").Text())

		if href == "" || title == "" {
			return
		}

		// Filter out ad/tracking URLs
		if strings.Contains(href, "duckduckgo.com/y.js") ||
			strings.Contains(href, "ad_domain") ||
			strings.Contains(href, "ad_provider") ||
			strings.Contains(href, "click_metadata") ||
			strings.Contains(href, "ad_type") {
			return
		}

		// Handle DDG redirects - extract actual URL from redirect link
		if strings.HasPrefix(href, "/l/") {
			// DuckDuckGo redirect format: /l/?kh=-1&uddg=<actual_url>
			if parsed, err := url.Parse("https://html.duckduckgo.com" + href); err == nil {
				if uddg := parsed.Query().Get("uddg"); uddg != "" {
					if decoded, err := url.QueryUnescape(uddg); err == nil {
						href = decoded
					}
				} else {
					// If no uddg param, skip this redirect link
					return
				}
			} else {
				return
			}
		} else if strings.HasPrefix(href, "/") {
			// Other relative links - skip them as they're likely internal DDG links
			return
		}

		// Validate it's a proper HTTP(S) URL
		parsedURL, err := url.Parse(href)
		if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
			return
		}

		// Skip if it's still a DuckDuckGo domain (likely an ad or tracking page)
		if strings.Contains(parsedURL.Host, "duckduckgo.com") {
			return
			}

			results = append(results, Result{Title: title, Href: href, Body: snippet})
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
	// Limit to a large but bounded size to avoid pathological pages.
	// We now rely on an LLM summarization step to compress this further,
	// so this cap just protects against extremely large documents.
	// 200k characters ≈ ~57k–70k tokens of raw text; summarization will
	// reduce this further before it is sent to the article-generation LLM.
	maxChars := c.maxChars
	if maxChars <= 0 {
		maxChars = 128000
	}
	if len(text) > maxChars {
		text = text[:maxChars]
	}
	return strings.TrimSpace(text), nil
}
