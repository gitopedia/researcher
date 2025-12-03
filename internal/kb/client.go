// Package kb provides a client for the knowledge-base API.
package kb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"
)

// Client provides access to the knowledge-base API
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// Source represents a source document
type Source struct {
	ID        string   `json:"id"`
	URL       string   `json:"url"`
	Title     string   `json:"title"`
	Topic     string   `json:"topic"`
	Summary   string   `json:"summary"`
	Language  string   `json:"language,omitempty"`
	Model     string   `json:"model,omitempty"`
	CreatedAt string   `json:"created_at,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	Score     float32  `json:"score,omitempty"` // Similarity score from search
}

// SearchResult represents a search result
type SearchResult struct {
	ID        string   `json:"id"`
	URL       string   `json:"url,omitempty"`
	Title     string   `json:"title"`
	Topic     string   `json:"topic,omitempty"`
	Summary   string   `json:"summary"`
	Score     float32  `json:"score,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	Language  string   `json:"language,omitempty"`
	Model     string   `json:"model,omitempty"`
	CreatedAt string   `json:"created_at,omitempty"`
}

// SearchResponse is the response from search endpoints
type SearchResponse struct {
	Results []SearchResult `json:"results"`
	Count   int            `json:"count"`
}

// HealthResponse is the response from the health endpoint
type HealthResponse struct {
	Status       string `json:"status"`
	SourceCount  int    `json:"source_count"`
	ArticleCount int    `json:"article_count"`
	Version      string `json:"version"`
}

// NewClient creates a new knowledge-base client
func NewClient() *Client {
	baseURL := os.Getenv("KB_API_URL")
	if baseURL == "" {
		baseURL = "http://localhost:8081"
	}

	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// NewClientWithURL creates a new client with a specific base URL
func NewClientWithURL(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// Health checks the health of the knowledge-base API
func (c *Client) Health(ctx context.Context) (*HealthResponse, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/health", nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("health check failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("health check returned status %d", resp.StatusCode)
	}

	var health HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return nil, fmt.Errorf("failed to decode health response: %w", err)
	}

	return &health, nil
}

// StoreSource stores a source in the knowledge-base
func (c *Client) StoreSource(ctx context.Context, src Source) (string, error) {
	body, err := json.Marshal(src)
	if err != nil {
		return "", fmt.Errorf("failed to marshal source: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/sources", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("store source failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("store source failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	return result.ID, nil
}

// GetSource retrieves a source by ID
func (c *Client) GetSource(ctx context.Context, id string) (*Source, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/sources/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get source failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get source failed (status %d)", resp.StatusCode)
	}

	var src Source
	if err := json.NewDecoder(resp.Body).Decode(&src); err != nil {
		return nil, fmt.Errorf("failed to decode source: %w", err)
	}

	return &src, nil
}

// SearchSources performs a semantic search for sources
func (c *Client) SearchSources(ctx context.Context, query string, limit int, topicFilter string) ([]SearchResult, error) {
	// Build URL with query parameters
	u, _ := url.Parse(c.baseURL + "/sources/search")
	q := u.Query()
	q.Set("q", query)
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if topicFilter != "" {
		q.Set("topic", topicFilter)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("search sources failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("search sources failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	var searchResp SearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&searchResp); err != nil {
		return nil, fmt.Errorf("failed to decode search response: %w", err)
	}

	return searchResp.Results, nil
}

// GetSourcesByTopic retrieves all sources for a specific topic
func (c *Client) GetSourcesByTopic(ctx context.Context, topic string, limit int) ([]Source, error) {
	u, _ := url.Parse(c.baseURL + "/sources/topic/" + url.PathEscape(topic))
	if limit > 0 {
		q := u.Query()
		q.Set("limit", strconv.Itoa(limit))
		u.RawQuery = q.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get sources by topic failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get sources by topic failed (status %d)", resp.StatusCode)
	}

	var result struct {
		Sources []Source `json:"sources"`
		Count   int      `json:"count"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return result.Sources, nil
}

// SearchArticles performs a full-text search on articles
func (c *Client) SearchArticles(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	u, _ := url.Parse(c.baseURL + "/articles/search")
	q := u.Query()
	q.Set("q", query)
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("search articles failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("search articles failed (status %d)", resp.StatusCode)
	}

	var searchResp SearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&searchResp); err != nil {
		return nil, fmt.Errorf("failed to decode search response: %w", err)
	}

	return searchResp.Results, nil
}

// IsAvailable checks if the knowledge-base API is available
func (c *Client) IsAvailable(ctx context.Context) bool {
	health, err := c.Health(ctx)
	return err == nil && health.Status == "ok"
}

