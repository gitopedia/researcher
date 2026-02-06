package search

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	httpClient         *http.Client
	apiBaseURL         string
	maxChars           int
	maxResultsPerQuery int
	protectedDomains   *ProtectedDomains
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

	protectedPath := os.Getenv("PROTECTED_DOMAINS_FILE")
	if protectedPath == "" {
		protectedPath = "protected_domains.json"
	}
	pd, err := LoadProtectedDomains(protectedPath)
	if err != nil {
		log.Printf("Warning: failed to load protected domains from %s: %v", protectedPath, err)
		pd = NewProtectedDomains(protectedPath)
	}

	return &Client{
		httpClient:         &http.Client{Timeout: 10 * time.Second},
		apiBaseURL:         "https://api.duckduckgo.com/",
		maxChars:           maxChars,
		maxResultsPerQuery: maxResultsPerQuery,
		protectedDomains:   pd,
	}
}

// Search performs a DuckDuckGo search via HTML scraping (first page only)
func (c *Client) Search(query string) ([]Result, error) {
	return c.SearchPage(query, 0)
}

// SearchPage performs a DuckDuckGo search with pagination support
// page 0 = first page, page 1 = second page (offset 30), etc.
func (c *Client) SearchPage(query string, page int) ([]Result, error) {
	log.Printf("Searching for: %s (page %d)", query, page)

	// Sleep slightly to be polite
	time.Sleep(2 * time.Second)

	// DuckDuckGo uses &s= parameter for offset (each page has ~30 results)
	offset := page * 30
	u := fmt.Sprintf("https://html.duckduckgo.com/html/?q=%s", url.QueryEscape(query))
	if offset > 0 {
		u = fmt.Sprintf("%s&s=%d", u, offset)
	}

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
	const minAcceptableExtractedChars = 800

	log.Printf("Fetching content (headless): %s", targetURL)

	parsed, _ := url.Parse(targetURL)
	domain := ""
	if parsed != nil {
		domain = parsed.Hostname()
	}

	// Create allocator options
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),            // Required for Docker/root
		chromedp.Flag("disable-dev-shm-usage", true), // Prevent shared memory issues in Docker
		chromedp.Flag("ignore-certificate-errors", true),
	)

	// Create allocator context
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancelAlloc()

	// Create context
	ctx, cancelCtx := chromedp.NewContext(allocCtx)
	defer cancelCtx()

	// Set a timeout for the entire operation
	headlessTimeout := 45 * time.Second
	if domain != "" && c.protectedDomains != nil && c.protectedDomains.IsProtected(domain) {
		// Keep quality as much as possible while avoiding repeated 45s stalls on known-problem domains.
		// Try a short headless attempt, then fall back to HTTP quickly.
		headlessTimeout = 10 * time.Second
		log.Printf("Domain %s is protected; using shorter headless timeout (%v)", domain, headlessTimeout)
	}
	ctx, cancelTimeout := context.WithTimeout(ctx, headlessTimeout)
	defer cancelTimeout()

	var htmlContent string
	// Navigate and wait for content
	err := chromedp.Run(ctx,
		chromedp.Navigate(targetURL),
		chromedp.WaitReady("body"),
		chromedp.Sleep(2*time.Second),
		chromedp.OuterHTML("html", &htmlContent),
	)
	var headlessText string
	var headlessErr error
	if err != nil {
		// Record timeouts so we can avoid repeatedly paying the full headless timeout for this domain.
		if domain != "" && c.protectedDomains != nil && errors.Is(err, context.DeadlineExceeded) {
			c.protectedDomains.RecordTimeout(domain)
		}
		headlessErr = fmt.Errorf("headless fetch failed: %w", err)
	} else {
		if extracted, err := c.extractReadableTextFromHTML(htmlContent); err != nil {
			headlessErr = err
		} else {
			headlessText = extracted
		}
	}

	if len(strings.TrimSpace(headlessText)) >= minAcceptableExtractedChars {
		return headlessText, nil
	}
	if headlessErr != nil {
		log.Printf("Headless fetch failed for %s: %v; attempting HTTP fallback", targetURL, headlessErr)
	} else {
		log.Printf("Headless extraction too short for %s (%d chars); attempting HTTP fallback",
			targetURL, len(strings.TrimSpace(headlessText)))
	}

	httpHTML, httpErr := c.fetchHTMLHTTP(targetURL)
	if httpErr == nil {
		httpText, err := c.extractReadableTextFromHTML(httpHTML)
		if err == nil && strings.TrimSpace(httpText) != "" {
			return httpText, nil
		}
		if err != nil {
			httpErr = err
		} else {
			httpErr = fmt.Errorf("HTTP fallback produced empty extracted text")
		}
	}

	// Prefer returning the headless error when headless failed outright; otherwise return the HTTP error.
	if headlessErr != nil {
		return "", headlessErr
	}
	return "", httpErr
}

func (c *Client) fetchHTMLHTTP(targetURL string) (string, error) {
	req, err := http.NewRequest("GET", targetURL, nil)
	if err != nil {
		return "", err
	}
	// Use a realistic browser UA; some sites serve JS shells to "HeadlessChrome".
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	// Use a slightly longer timeout than the search client default (content pages can be slower).
	client := c.httpClient
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	} else if client.Timeout < 20*time.Second {
		client = &http.Client{Timeout: 20 * time.Second}
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP fallback bad status: %s", resp.Status)
	}

	// Guardrail: cap HTML read size.
	const maxHTMLBytes = 5 * 1024 * 1024
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxHTMLBytes))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (c *Client) extractReadableTextFromHTML(htmlContent string) (string, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		return "", err
	}

	// Remove sections that typically contain noise (references, external links, etc.)
	// This works for Wikipedia and many other encyclopedia-style sites
	doc.Find("#References, #Notes, #Citations, #External_links, #See_also, #Further_reading").Each(func(i int, s *goquery.Selection) {
		// Remove the heading and all content until the next h2
		s.Parent().NextUntil("h2").Remove()
		s.Parent().Remove()
	})

	// Also remove by class names commonly used for references
	doc.Find(".reflist, .references, .citation, .navbox, .sidebar, .infobox, .toc, #toc").Remove()

	extractFromRoot := func(root *goquery.Selection) string {
		var b strings.Builder
		root.Find("h1, h2, h3, h4, p, li").Each(func(i int, s *goquery.Selection) {
			// Skip items inside reference lists or navigation
			if s.ParentsFiltered(".reflist, .references, .navbox, .sidebar, nav, footer, .toc").Length() > 0 {
				return
			}
			// Normalize whitespace
			text := strings.Join(strings.Fields(s.Text()), " ")
			if len(text) > 20 { // Filter out very short snippets/nav items
				b.WriteString(text + "\n\n")
			}
		})
		return b.String()
	}

	// Many modern sites have multiple candidate "content" containers; picking `.First()` can land on
	// a cookie banner or navigation block. Try all candidates and keep the largest extraction.
	candidateSelector := "article, main, .content, #content, #bodyContent, .entry-content, .post-content, .post-body, .article-content, .page-content"
	best := ""
	doc.Find(candidateSelector).Each(func(i int, root *goquery.Selection) {
		txt := extractFromRoot(root)
		if len(txt) > len(best) {
			best = txt
		}
	})

	// Fallback: if no candidate containers produced anything, use the whole document.
	if strings.TrimSpace(best) == "" {
		best = extractFromRoot(doc.Selection)
	}

	text := best

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
