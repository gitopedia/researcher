package search

import (
	"os"
	"testing"
)

func TestSearch(t *testing.T) {
	// Skip if running in CI or if we want to avoid hitting real DuckDuckGo
	if os.Getenv("CI") != "" || os.Getenv("SKIP_INTEGRATION_TESTS") != "" {
		t.Skip("Skipping integration test that hits DuckDuckGo")
	}

	c := NewClient()
	results, err := c.Search("golang")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	// Just verify we got some results - DuckDuckGo results can vary
	if len(results) == 0 {
		t.Error("Expected at least 1 result, got 0")
	}

	// Verify result structure
	for _, r := range results {
		if r.Title == "" {
			t.Error("Result missing title")
		}
		if r.Href == "" {
			t.Error("Result missing href")
		}
	}
}
