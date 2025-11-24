package github

import (
	"github.com/google/go-github/v57/github"
)

type GitHubClient interface {
	GetResearchRequests() ([]*github.Issue, error)
	CreateBranch(baseBranch, newBranch string) error
	CreateFile(branch, path, message, content string) error
	UpdateFile(branch, path, message, content, sha string) error
	CreatePullRequest(title, body, head, base string) (*github.PullRequest, error)
	CommentOnIssue(issueNumber int, body string) error
	GetFile(ref, path string) (string, string, error)
}

// Ensure Client implements GitHubClient
var _ GitHubClient = &Client{}
