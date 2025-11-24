package search

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
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

// Search performs a DuckDuckGo search
func (c *Client) Search(query string) ([]Result, error) {
	fmt.Printf("Searching for: %s\n", query)

	// Use DDG Instant Answer API
	u := fmt.Sprintf("%s?q=%s&format=json", c.apiBaseURL, url.QueryEscape(query))
	resp, err := c.httpClient.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var ddgResp struct {
		AbstractText  string `json:"AbstractText"`
		AbstractURL   string `json:"AbstractURL"`
		RelatedTopics []struct {
			Text     string `json:"Text"`
			FirstURL string `json:"FirstURL"`
		} `json:"RelatedTopics"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&ddgResp); err != nil {
		return nil, err
	}

	var results []Result

	// If we have an abstract, that's a great primary source
	if ddgResp.AbstractText != "" {
		results = append(results, Result{
			Title: query + " - Abstract",
			Href:  ddgResp.AbstractURL,
			Body:  ddgResp.AbstractText,
		})
	}

	// Add related topics as results
	for i, topic := range ddgResp.RelatedTopics {
		if i >= 3 {
			break
		}
		if topic.Text != "" {
			results = append(results, Result{
				Title: "Related: " + topic.Text, // Simplification
				Href:  topic.FirstURL,
				Body:  topic.Text,
			})
		}
	}

	return results, nil
}
