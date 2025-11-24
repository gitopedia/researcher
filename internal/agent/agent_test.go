package agent

import (
	"context"
	"testing"

	"github.com/gitopedia/researcher/internal/search"
	gh "github.com/google/go-github/v57/github"
)

// Mocks
type MockGitHub struct{}

func (m *MockGitHub) GetResearchRequests() ([]*gh.Issue, error) {
	title := "Article Request: Test Topic"
	number := 1
	return []*gh.Issue{{Title: &title, Number: &number}}, nil
}
func (m *MockGitHub) CreateBranch(base, new string) error  { return nil }
func (m *MockGitHub) CreateFile(b, p, msg, c string) error { return nil }
func (m *MockGitHub) CreatePullRequest(t, b, h, base string) (*gh.PullRequest, error) {
	num := 100
	url := "http://pr"
	return &gh.PullRequest{Number: &num, HTMLURL: &url}, nil
}
func (m *MockGitHub) CommentOnIssue(n int, b string) error { return nil }

type MockSearch struct{}

func (m *MockSearch) Search(q string) ([]search.Result, error) {
	return []search.Result{{Title: "Res", Body: "Content"}}, nil
}

type MockLLM struct{}

func (m *MockLLM) GenerateArticle(ctx context.Context, t, c string) (string, error) {
	return "# Article\nContent", nil
}

func TestAgentRun(t *testing.T) {
	agent := NewAgentWithDeps(&MockGitHub{}, &MockSearch{}, &MockLLM{})
	if err := agent.Run(context.Background()); err != nil {
		t.Errorf("Agent.Run failed: %v", err)
	}
}
