package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
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
	var token string
	var err error

	// Try GitHub App authentication first
	appID := os.Getenv("GITHUB_APP_ID")
	if appID != "" {
		token, err = getAppInstallationToken(ctx, appID)
		if err != nil {
			return nil, fmt.Errorf("failed to get app installation token: %w", err)
		}
	} else {
		// Fall back to PAT
		token = os.Getenv("GITHUB_TOKEN")
		if token == "" {
			return nil, fmt.Errorf("either GITHUB_APP_ID or GITHUB_TOKEN environment variable must be set")
		}
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

func getAppInstallationToken(ctx context.Context, appID string) (string, error) {
	// Read private key
	keyPath := os.Getenv("GITHUB_APP_PRIVATE_KEY_PATH")
	var keyBytes []byte
	var err error

	if keyPath != "" {
		keyBytes, err = os.ReadFile(keyPath)
		if err != nil {
			return "", fmt.Errorf("failed to read private key from %s: %w", keyPath, err)
		}
	} else {
		// Try reading from environment variable directly
		keyContent := os.Getenv("GITHUB_APP_PRIVATE_KEY")
		if keyContent == "" {
			return "", fmt.Errorf("GITHUB_APP_PRIVATE_KEY_PATH or GITHUB_APP_PRIVATE_KEY must be set")
		}
		// Handle escaped newlines in environment variable (common when pasting keys)
		keyContent = strings.ReplaceAll(keyContent, "\\n", "\n")
		keyBytes = []byte(keyContent)
	}

	// Generate JWT
	jwtToken, err := generateJWT(appID, keyBytes)
	if err != nil {
		return "", fmt.Errorf("failed to generate JWT: %w", err)
	}

	// Get installation ID
	installID := os.Getenv("GITHUB_APP_INSTALLATION_ID")
	if installID == "" {
		// Auto-detect installation ID
		installID, err = getInstallationID(ctx, jwtToken)
		if err != nil {
			return "", fmt.Errorf("failed to get installation ID: %w", err)
		}
	}

	// Get installation token
	token, err := fetchInstallationToken(ctx, jwtToken, installID)
	if err != nil {
		return "", fmt.Errorf("failed to fetch installation token: %w", err)
	}

	return token, nil
}

func generateJWT(appID string, keyBytes []byte) (string, error) {
	key, err := jwt.ParseRSAPrivateKeyFromPEM(keyBytes)
	if err != nil {
		return "", err
	}

	now := time.Now()
	claims := jwt.MapClaims{
		"iat": now.Add(-60 * time.Second).Unix(),
		"exp": now.Add(10 * time.Minute).Unix(),
		"iss": appID,
	}

	t := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return t.SignedString(key)
}

func getInstallationID(ctx context.Context, jwtToken string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/app/installations", nil)
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", "Bearer "+jwtToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	var installations []struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(body, &installations); err != nil {
		return "", err
	}

	if len(installations) == 0 {
		return "", fmt.Errorf("no installations found for this app")
	}

	// Return the first installation ID
	return strconv.FormatInt(installations[0].ID, 10), nil
}

func fetchInstallationToken(ctx context.Context, jwtToken, installID string) (string, error) {
	url := fmt.Sprintf("https://api.github.com/app/installations/%s/access_tokens", installID)
	req, err := http.NewRequestWithContext(ctx, "POST", url, nil)
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", "Bearer "+jwtToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	var res struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return "", err
	}
	return res.Token, nil
}

func (c *Client) GetResearchRequests() ([]*github.Issue, error) {
	opts := &github.IssueListByRepoOptions{
		State:  "open",
		Labels: []string{"research category"},
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

func (c *Client) UpdateFile(branch, path, message, content, sha string) error {
	opts := &github.RepositoryContentFileOptions{
		Message: github.String(message),
		Content: []byte(content),
		Branch:  github.String(branch),
		SHA:     github.String(sha),
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

func (c *Client) GetFile(ref, path string) (string, string, error) {
	opts := &github.RepositoryContentGetOptions{
		Ref: ref,
	}
	fileContent, _, _, err := c.client.Repositories.GetContents(c.ctx, c.owner, c.repo, path, opts)
	if err != nil {
		return "", "", err
	}
	if fileContent == nil {
		return "", "", fmt.Errorf("file content is nil")
	}
	content, err := fileContent.GetContent()
	if err != nil {
		return "", "", err
	}
	return content, fileContent.GetSHA(), nil
}

func (c *Client) ListAllFiles(path string) ([]string, error) {
	// Get default branch SHA
	repo, _, err := c.client.Repositories.Get(c.ctx, c.owner, c.repo)
	if err != nil {
		return nil, err
	}
	defaultBranch := repo.GetDefaultBranch()

	tree, _, err := c.client.Git.GetTree(c.ctx, c.owner, c.repo, defaultBranch, true)
	if err != nil {
		return nil, err
	}

	var files []string
	for _, entry := range tree.Entries {
		if entry.GetType() == "blob" && strings.HasPrefix(entry.GetPath(), path) {
			files = append(files, entry.GetPath())
		}
	}
	return files, nil
}

func (c *Client) CreateIssue(title, body string, labels []string) (*github.Issue, error) {
	req := &github.IssueRequest{
		Title:  github.String(title),
		Body:   github.String(body),
		Labels: &labels,
	}
	issue, _, err := c.client.Issues.Create(c.ctx, c.owner, c.repo, req)
	return issue, err
}

func (c *Client) GetPRStatus(prNumber int) (*PRStatus, error) {
	pr, _, err := c.client.PullRequests.Get(c.ctx, c.owner, c.repo, prNumber)
	if err != nil {
		return nil, err
	}

	status := &PRStatus{
		Number:    prNumber,
		Draft:     pr.GetDraft(),
		Merged:    pr.GetMerged(),
		Mergeable: pr.GetMergeable(),
		State:     pr.GetState(),
		CIStatus:  "pending",
	}

	// Get combined status for the head SHA
	if pr.Head != nil && pr.Head.SHA != nil {
		combined, _, err := c.client.Repositories.GetCombinedStatus(c.ctx, c.owner, c.repo, *pr.Head.SHA, nil)
		if err == nil && combined != nil {
			status.CIStatus = combined.GetState()
		}

		// Also check check runs (GitHub Actions uses check runs, not statuses)
		checkRuns, _, err := c.client.Checks.ListCheckRunsForRef(c.ctx, c.owner, c.repo, *pr.Head.SHA, nil)
		if err == nil && checkRuns != nil && len(checkRuns.CheckRuns) > 0 {
			allSuccess := true
			anyFailure := false
			anyPending := false

			for _, run := range checkRuns.CheckRuns {
				switch run.GetConclusion() {
				case "success", "skipped", "neutral":
					// OK
				case "failure", "cancelled", "timed_out", "action_required":
					anyFailure = true
					allSuccess = false
				default:
					if run.GetStatus() != "completed" {
						anyPending = true
						allSuccess = false
					}
				}
			}

			if anyFailure {
				status.CIStatus = "failure"
			} else if anyPending {
				status.CIStatus = "pending"
			} else if allSuccess {
				status.CIStatus = "success"
			}
		}
	}

	return status, nil
}

func (c *Client) MergePR(prNumber int, commitMessage string) error {
	opts := &github.PullRequestOptions{
		CommitTitle: commitMessage,
		MergeMethod: "squash",
	}
	_, _, err := c.client.PullRequests.Merge(c.ctx, c.owner, c.repo, prNumber, commitMessage, opts)
	return err
}

func (c *Client) CommentOnPR(prNumber int, body string) error {
	// PRs are issues in GitHub's API
	return c.CommentOnIssue(prNumber, body)
}

func (c *Client) CloseIssue(issueNumber int) error {
	state := "closed"
	req := &github.IssueRequest{
		State: &state,
	}
	_, _, err := c.client.Issues.Edit(c.ctx, c.owner, c.repo, issueNumber, req)
	return err
}
