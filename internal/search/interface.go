package search

type Searcher interface {
	Search(query string) ([]Result, error)
	FetchContent(url string) (string, error)
}

// Ensure Client implements Searcher
var _ Searcher = &Client{}
