package search

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSearch(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"AbstractText": "Go is a language.",
			"AbstractURL": "https://golang.org",
			"RelatedTopics": [
				{"Text": "Go Game", "FirstURL": "https://game.com"}
			]
		}`))
	}))
	defer ts.Close()

	c := NewClient()
	c.apiBaseURL = ts.URL + "/" // Override for testing

	results, err := c.Search("golang")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("Expected 2 results, got %d", len(results))
	}

	if results[0].Body != "Go is a language." {
		t.Errorf("Unexpected result body: %s", results[0].Body)
	}
}
