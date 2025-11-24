package github

import (
	"context"
	"fmt"
	"os"

	"github.com/google/go-github/v57/github"
	"golang.org/x/oauth2"
)

type Client struct {
	client *github.Client
	ctx    context.Context
	owner  string
	repo   string
}

func NewClient(ctx context.Context) (*Client, error) {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("GITHUB_TOKEN environment variable not set")
	}

	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)

	return &Client{
		client: client,
		ctx:    ctx,
		owner:  "gitopedia", // Default, can be made configurable
		repo:   "gitopedia",
	}, nil
}

func (c *Client) GetResearchRequests() ([]*github.Issue, error) {
	opts := &github.IssueListByRepoOptions{
		State:  "open",
		Labels: []string{"research request"},
	}
	issues, _, err := c.client.Issues.ListByRepo(c.ctx, c.owner, c.repo, opts)
	return issues, err
}

func (c *Client) CreateBranch(baseBranch, newBranch string) error {
	// Get reference of base branch
	ref, _, err := c.client.Git.GetRef(c.ctx, c.owner, c.repo, "heads/"+baseBranch)
	if err != nil {
		return err
	}

	// Create new branch
	newRef := &github.Reference{
		Ref: github.String("refs/heads/" + newBranch),
		Object: &github.GitObject{
			SHA: ref.Object.SHA,
		},
	}
	_, _, err = c.client.Git.CreateRef(c.ctx, c.owner, c.repo, newRef)
	return err
}

func (c *Client) CreateFile(branch, path, message, content string) error {
	opts := &github.RepositoryContentFileOptions{
		Message: github.String(message),
		Content: []byte(content),
		Branch:  github.String(branch),
	}
	_, _, err := c.client.Repositories.CreateFile(c.ctx, c.owner, c.repo, path, opts)
	return err
}

func (c *Client) CreatePullRequest(title, body, head, base string) (*github.PullRequest, error) {
	newPR := &github.NewPullRequest{
		Title: github.String(title),
		Body:  github.String(body),
		Head:  github.String(head),
		Base:  github.String(base),
		Draft: github.Bool(true),
	}
	pr, _, err := c.client.PullRequests.Create(c.ctx, c.owner, c.repo, newPR)
	return pr, err
}

func (c *Client) CommentOnIssue(issueNumber int, body string) error {
	comment := &github.IssueComment{
		Body: github.String(body),
	}
	_, _, err := c.client.Issues.CreateComment(c.ctx, c.owner, c.repo, issueNumber, comment)
	return err
}
