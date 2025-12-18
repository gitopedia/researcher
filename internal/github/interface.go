package github

import (
	"github.com/google/go-github/v57/github"
)

// PRStatus represents the current state of a pull request
type PRStatus struct {
	Number    int
	Draft     bool
	Merged    bool
	Mergeable *bool  // nil = not yet computed, true = mergeable, false = has conflicts
	State     string // "open", "closed"
	CIStatus  string // "pending", "success", "failure"
}

type GitHubClient interface {
	GetResearchRequests() ([]*github.Issue, error)
	ListAllOpenIssues() ([]*github.Issue, error)
	CreateBranch(baseBranch, newBranch string) error
	ListBranches() ([]*github.Branch, error)
	DeleteBranch(branchName string) error
	CreateFile(branch, path, message, content string) error
	UpdateFile(branch, path, message, content, sha string) error
	CreatePullRequest(title, body, head, base string) (*github.PullRequest, error)
	CommentOnIssue(issueNumber int, body string) error
	GetFile(ref, path string) (string, string, error)
	ListAllFiles(path string) ([]string, error)
	CreateIssue(title, body string, labels []string) (*github.Issue, error)

	// PR monitoring and management
	GetPRStatus(prNumber int) (*PRStatus, error)
	MergePR(prNumber int, commitMessage string) error
	ClosePR(prNumber int) error
	CommentOnPR(prNumber int, body string) error
	CloseIssue(issueNumber int) error
	ReopenIssue(issueNumber int) error
	ListClosedIssuesWithLabel(label string, limit int) ([]*github.Issue, error)
	UpdatePRBranch(prNumber int) error                       // Merge base branch into PR branch to resolve conflicts
	CreateMergeCommitWithResolution(headBranch string) error // Create merge commit resolving authority/index conflicts

	// CI monitoring
	GetFailedCILogs(prNumber int) (string, error) // Get logs from failed CI runs for a PR

	// PR listing
	ListOpenPRs() ([]*PRInfo, error)

	// File operations for organizer
	ListFilesInBranch(branch, path string) ([]string, error)
	DeleteFile(branch, path, message, sha string) error
	MarkPRReady(prNumber int) error

	// Label management
	AddLabel(issueNumber int, label string) error
	RemoveLabel(issueNumber int, label string) error
	HasLabel(issueNumber int, label string) (bool, error)

	// Mode checks
	IsLocal() bool
}

// PRInfo contains basic info about a PR
type PRInfo struct {
	Number     int
	Title      string
	Body       string
	Draft      bool
	IssueRefs  []int  // Issue numbers referenced in the PR body
	HeadBranch string // The branch name of the PR
}

// Ensure Client implements GitHubClient
var _ GitHubClient = &Client{}
