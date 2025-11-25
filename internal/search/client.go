package search

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
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

func (c *Client) FetchContent(url string) (string, error) {
	fmt.Printf("Fetching content: %s\n", url)
	resp, err := c.httpClient.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("bad status: %s", resp.Status)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return "", err
	}

	// Extract readable text (paragraphs)
	var textBuilder strings.Builder
	doc.Find("p").Each(func(i int, s *goquery.Selection) {
		text := strings.TrimSpace(s.Text())
		if text != "" {
			textBuilder.WriteString(text + "\n\n")
		}
	})

	text := textBuilder.String()
	// Limit to 5000 chars
	if len(text) > 5000 {
		text = text[:5000] + "..."
	}
	return strings.TrimSpace(text), nil
}
