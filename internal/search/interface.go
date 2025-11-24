package search

type Searcher interface {
	Search(query string) ([]Result, error)
}

// Ensure Client implements Searcher
var _ Searcher = &Client{}
