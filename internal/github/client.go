package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
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
	client      *github.Client
	ctx         context.Context
	owner       string
	repo        string
	tokenExpiry time.Time
	useAppAuth  bool
}

func NewClient(ctx context.Context) (*Client, error) {
	c := &Client{
		ctx:   ctx,
		owner: "gitopedia", // Default, can be made configurable
		repo:  "gitopedia",
	}

	// Check if using GitHub App auth
	c.useAppAuth = os.Getenv("GITHUB_APP_ID") != ""

	if err := c.refreshClient(); err != nil {
		return nil, err
	}

	return c, nil
}

// refreshClient creates a new authenticated GitHub client
func (c *Client) refreshClient() error {
	var token string
	var err error

	// Try GitHub App authentication first
	appID := os.Getenv("GITHUB_APP_ID")
	if appID != "" {
		token, err = getAppInstallationToken(c.ctx, appID)
		if err != nil {
			return fmt.Errorf("failed to get app installation token: %w", err)
		}
		// GitHub App tokens expire after 1 hour, refresh after 45 minutes to be safe
		c.tokenExpiry = time.Now().Add(45 * time.Minute)
		log.Printf("GitHub App token refreshed, expires in 45 minutes")
	} else {
		// Fall back to PAT
		token = os.Getenv("GITHUB_TOKEN")
		if token == "" {
			return fmt.Errorf("either GITHUB_APP_ID or GITHUB_TOKEN environment variable must be set")
		}
		// PATs don't expire during runtime
		c.tokenExpiry = time.Now().Add(24 * time.Hour)
	}

	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(c.ctx, ts)
	c.client = github.NewClient(tc)

	return nil
}

// ensureValidToken refreshes the token if it's expired or about to expire
func (c *Client) ensureValidToken() error {
	if c.useAppAuth && time.Now().After(c.tokenExpiry) {
		log.Println("Token expired, refreshing...")
		return c.refreshClient()
	}
	return nil
}

// ForceRefreshToken forces a token refresh, useful after 401 errors
func (c *Client) ForceRefreshToken() error {
	if c.useAppAuth {
		log.Println("Forcing token refresh...")
		c.tokenExpiry = time.Time{} // Expire immediately
		return c.refreshClient()
	}
	return nil
}

// is401Error checks if an error is a 401 Unauthorized error
func is401Error(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "401") && strings.Contains(err.Error(), "Bad credentials")
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
	if err := c.ensureValidToken(); err != nil {
		return nil, fmt.Errorf("failed to refresh token: %w", err)
	}

	// Use pagination to get all issues with the "research category" label
	opts := &github.IssueListByRepoOptions{
		State:  "open",
		Labels: []string{"research category"},
		ListOptions: github.ListOptions{
			PerPage: 100,
		},
	}

	var allIssues []*github.Issue
	for {
		issues, resp, err := c.client.Issues.ListByRepo(c.ctx, c.owner, c.repo, opts)
		if err != nil {
			return nil, err
		}
		allIssues = append(allIssues, issues...)
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	return allIssues, nil
}

func (c *Client) ListAllOpenIssues() ([]*github.Issue, error) {
	if err := c.ensureValidToken(); err != nil {
		return nil, fmt.Errorf("failed to refresh token: %w", err)
	}

	opts := &github.IssueListByRepoOptions{
		State: "open",
		ListOptions: github.ListOptions{
			PerPage: 100,
		},
	}

	var allIssues []*github.Issue
	for {
		issues, resp, err := c.client.Issues.ListByRepo(c.ctx, c.owner, c.repo, opts)
		if err != nil {
			return nil, err
		}
		allIssues = append(allIssues, issues...)
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	return allIssues, nil
}

func (c *Client) CreateBranch(baseBranch, newBranch string) error {
	if err := c.ensureValidToken(); err != nil {
		return fmt.Errorf("failed to refresh token: %w", err)
	}

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

func (c *Client) ListBranches() ([]*github.Branch, error) {
	if err := c.ensureValidToken(); err != nil {
		return nil, fmt.Errorf("failed to refresh token: %w", err)
	}

	var allBranches []*github.Branch
	opts := &github.BranchListOptions{
		ListOptions: github.ListOptions{
			PerPage: 100,
		},
	}

	for {
		branches, resp, err := c.client.Repositories.ListBranches(c.ctx, c.owner, c.repo, opts)
		if err != nil {
			return nil, err
		}
		allBranches = append(allBranches, branches...)
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	return allBranches, nil
}

func (c *Client) DeleteBranch(branchName string) error {
	if err := c.ensureValidToken(); err != nil {
		return fmt.Errorf("failed to refresh token: %w", err)
	}

	ref := fmt.Sprintf("heads/%s", branchName)
	_, err := c.client.Git.DeleteRef(c.ctx, c.owner, c.repo, ref)
	return err
}

func (c *Client) CreateFile(branch, path, message, content string) error {
	if err := c.ensureValidToken(); err != nil {
		return fmt.Errorf("failed to refresh token: %w", err)
	}

	opts := &github.RepositoryContentFileOptions{
		Message: github.String(message),
		Content: []byte(content),
		Branch:  github.String(branch),
	}
	_, _, err := c.client.Repositories.CreateFile(c.ctx, c.owner, c.repo, path, opts)

	// If 401 error, try refreshing token and retry once
	if is401Error(err) {
		log.Printf("Got 401 error, refreshing token and retrying...")
		if refreshErr := c.ForceRefreshToken(); refreshErr != nil {
			return fmt.Errorf("failed to refresh token after 401: %w", refreshErr)
		}
		_, _, err = c.client.Repositories.CreateFile(c.ctx, c.owner, c.repo, path, opts)
	}

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
		Mergeable: pr.Mergeable, // Access field directly to preserve nil (unknown) vs false (conflicts)
		State:     pr.GetState(),
		CIStatus:  "success", // Default to success if no CI configured
	}

	// Get combined status for the head SHA
	if pr.Head != nil && pr.Head.SHA != nil {
		hasChecks := false

		// Check combined commit status (older status API)
		combined, _, err := c.client.Repositories.GetCombinedStatus(c.ctx, c.owner, c.repo, *pr.Head.SHA, nil)
		if err == nil && combined != nil && len(combined.Statuses) > 0 {
			hasChecks = true
			status.CIStatus = combined.GetState()
		}

		// Also check check runs (GitHub Actions uses check runs, not statuses)
		checkRuns, _, err := c.client.Checks.ListCheckRunsForRef(c.ctx, c.owner, c.repo, *pr.Head.SHA, nil)
		if err == nil && checkRuns != nil && len(checkRuns.CheckRuns) > 0 {
			hasChecks = true
			allSuccess := true
			anyFailure := false
			anyPending := false

			for _, run := range checkRuns.CheckRuns {
				runStatus := run.GetStatus()
				conclusion := run.GetConclusion()

				// Check if still running
				if runStatus == "in_progress" || runStatus == "queued" || runStatus == "waiting" {
					anyPending = true
					allSuccess = false
					continue
				}

				// Check completed runs
				if runStatus == "completed" {
					switch conclusion {
					case "success", "skipped", "neutral":
						// OK
					case "failure", "cancelled", "timed_out", "action_required":
						anyFailure = true
						allSuccess = false
					default:
						// Unknown conclusion on completed run - treat as pending
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

		// If no checks at all, it's considered passing (no CI required)
		if !hasChecks {
			status.CIStatus = "success"
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

func (c *Client) ClosePR(prNumber int) error {
	if err := c.ensureValidToken(); err != nil {
		return fmt.Errorf("failed to refresh token: %w", err)
	}
	state := "closed"
	_, _, err := c.client.PullRequests.Edit(c.ctx, c.owner, c.repo, prNumber, &github.PullRequest{
		State: &state,
	})
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

func (c *Client) ReopenIssue(issueNumber int) error {
	if err := c.ensureValidToken(); err != nil {
		return fmt.Errorf("failed to refresh token: %w", err)
	}
	state := "open"
	req := &github.IssueRequest{
		State: &state,
	}
	_, _, err := c.client.Issues.Edit(c.ctx, c.owner, c.repo, issueNumber, req)
	return err
}

func (c *Client) ListClosedIssuesWithLabel(label string, limit int) ([]*github.Issue, error) {
	if err := c.ensureValidToken(); err != nil {
		return nil, fmt.Errorf("failed to refresh token: %w", err)
	}

	opts := &github.IssueListByRepoOptions{
		State:     "closed",
		Labels:    []string{label},
		Sort:      "updated",
		Direction: "desc",
		ListOptions: github.ListOptions{
			PerPage: limit,
		},
	}
	issues, _, err := c.client.Issues.ListByRepo(c.ctx, c.owner, c.repo, opts)
	return issues, err
}

// GetFailedCILogs retrieves logs from failed CI workflow runs for a PR.
// It returns a summary of failed jobs and their error messages.
func (c *Client) GetFailedCILogs(prNumber int) (string, error) {
	if err := c.ensureValidToken(); err != nil {
		return "", fmt.Errorf("failed to refresh token: %w", err)
	}

	// Get the PR to find the head SHA
	pr, _, err := c.client.PullRequests.Get(c.ctx, c.owner, c.repo, prNumber)
	if err != nil {
		return "", fmt.Errorf("failed to get PR: %w", err)
	}
	headSHA := pr.GetHead().GetSHA()

	// List check runs for the commit
	checkRuns, _, err := c.client.Checks.ListCheckRunsForRef(c.ctx, c.owner, c.repo, headSHA, &github.ListCheckRunsOptions{
		Status:      github.String("completed"),
		ListOptions: github.ListOptions{PerPage: 50},
	})
	if err != nil {
		return "", fmt.Errorf("failed to list check runs: %w", err)
	}

	var failedLogs strings.Builder
	for _, run := range checkRuns.CheckRuns {
		if run.GetConclusion() == "failure" {
			failedLogs.WriteString(fmt.Sprintf("## Failed: %s\n", run.GetName()))

			// Try to get annotations which often contain error details
			annotations, _, err := c.client.Checks.ListCheckRunAnnotations(c.ctx, c.owner, c.repo, run.GetID(), &github.ListOptions{PerPage: 50})
			if err == nil && len(annotations) > 0 {
				for _, ann := range annotations {
					failedLogs.WriteString(fmt.Sprintf("- %s:%d: %s\n", ann.GetPath(), ann.GetStartLine(), ann.GetMessage()))
				}
			}

			// Include the output summary if available
			if run.Output != nil && run.Output.Summary != nil {
				summary := run.Output.GetSummary()
				if len(summary) > 500 {
					summary = summary[:500] + "..."
				}
				failedLogs.WriteString(fmt.Sprintf("Summary: %s\n", summary))
			}

			// Include the output text if available (often contains error details)
			if run.Output != nil && run.Output.Text != nil {
				text := run.Output.GetText()
				if len(text) > 1000 {
					text = text[:1000] + "..."
				}
				failedLogs.WriteString(fmt.Sprintf("Details:\n%s\n", text))
			}

			failedLogs.WriteString("\n")
		}
	}

	if failedLogs.Len() == 0 {
		return "No failed CI runs found", nil
	}

	return failedLogs.String(), nil
}

// UpdatePRBranch updates the PR branch by merging the base branch (main) into it.
// This resolves conflicts when the PR is behind the base branch.
func (c *Client) UpdatePRBranch(prNumber int) error {
	if err := c.ensureValidToken(); err != nil {
		return fmt.Errorf("failed to refresh token: %w", err)
	}

	// Use the UpdateBranch API to merge base into the PR branch
	opts := &github.PullRequestBranchUpdateOptions{
		ExpectedHeadSHA: nil, // Accept any current HEAD
	}
	_, _, err := c.client.PullRequests.UpdateBranch(c.ctx, c.owner, c.repo, prNumber, opts)
	return err
}

// AuthorityEntry represents an entry in an authority JSON file
type AuthorityEntry struct {
	ID      string   `json:"id"`
	Label   string   `json:"label"`
	Aliases []string `json:"aliases"`
}

// CreateMergeCommitWithResolution creates a merge commit that properly merges main into
// the PR branch, resolving conflicts in authority files and index.md files.
// This uses the Git Data API to create a commit with two parents.
func (c *Client) CreateMergeCommitWithResolution(branch string) error {
	log.Printf("[MergeCommit] Starting merge commit creation for branch: %s", branch)

	if err := c.ensureValidToken(); err != nil {
		return fmt.Errorf("failed to refresh token: %w", err)
	}

	// Get the SHA of main's HEAD
	mainRef, _, err := c.client.Git.GetRef(c.ctx, c.owner, c.repo, "heads/main")
	if err != nil {
		return fmt.Errorf("failed to get main ref: %w", err)
	}
	mainSHA := mainRef.Object.GetSHA()
	log.Printf("[MergeCommit] Main HEAD: %s", mainSHA[:7])

	// Get the SHA of the branch's HEAD
	branchRef, _, err := c.client.Git.GetRef(c.ctx, c.owner, c.repo, "heads/"+branch)
	if err != nil {
		return fmt.Errorf("failed to get branch ref: %w", err)
	}
	branchSHA := branchRef.Object.GetSHA()
	log.Printf("[MergeCommit] Branch HEAD: %s", branchSHA[:7])

	log.Printf("[MergeCommit] Creating merge commit: main(%s) + %s(%s)", mainSHA[:7], branch, branchSHA[:7])

	// Get the tree from main (this will be our base)
	mainCommit, _, err := c.client.Git.GetCommit(c.ctx, c.owner, c.repo, mainSHA)
	if err != nil {
		return fmt.Errorf("failed to get main commit: %w", err)
	}
	baseTreeSHA := mainCommit.Tree.GetSHA()
	log.Printf("[MergeCommit] Base tree SHA: %s", baseTreeSHA[:7])

	// Get all files from the PR branch that need to be added/merged
	log.Printf("[MergeCommit] Listing files in branch: %s", branch)
	branchFiles, err := c.ListFilesInBranch(branch, "")
	if err != nil {
		return fmt.Errorf("failed to list branch files: %w", err)
	}
	log.Printf("[MergeCommit] Branch has %d files", len(branchFiles))

	log.Printf("[MergeCommit] Listing files in main")
	mainFiles, err := c.ListFilesInBranch("main", "")
	if err != nil {
		return fmt.Errorf("failed to list main files: %w", err)
	}
	log.Printf("[MergeCommit] Main has %d files", len(mainFiles))

	// Build a set of main files for quick lookup
	mainFileSet := make(map[string]bool)
	for _, f := range mainFiles {
		mainFileSet[f] = true
	}

	// Collect tree entries for files that need special handling
	var treeEntries []*github.TreeEntry
	newFilesCount := 0

	// 1. Add files from PR branch that don't exist in main (new articles, sources, etc.)
	log.Printf("[MergeCommit] Step 1: Adding new files from branch...")
	for _, file := range branchFiles {
		if mainFileSet[file] {
			continue // File exists in both - handle conflicts separately
		}

		// Get file content from branch
		content, _, err := c.GetFile(branch, file)
		if err != nil {
			log.Printf("[MergeCommit] Warning: could not get %s from branch: %v", file, err)
			continue
		}

		mode := "100644" // Regular file
		fileType := "blob"
		treeEntries = append(treeEntries, &github.TreeEntry{
			Path:    github.String(file),
			Mode:    github.String(mode),
			Type:    github.String(fileType),
			Content: github.String(content),
		})
		newFilesCount++
	}
	log.Printf("[MergeCommit] Added %d new files from branch", newFilesCount)

	// 2. Handle authority JSON files - merge entries from both branches
	log.Printf("[MergeCommit] Step 2: Merging authority JSON files...")
	authorityFiles := []string{
		"authority/topics.json",
		"authority/people.json",
		"authority/orgs.json",
		"authority/places.json",
	}

	for _, path := range authorityFiles {
		mainContent, _, err := c.GetFile("main", path)
		if err != nil {
			log.Printf("[MergeCommit] Skipping %s: not in main (%v)", path, err)
			continue
		}
		branchContent, _, err := c.GetFile(branch, path)
		if err != nil {
			log.Printf("[MergeCommit] Skipping %s: not in branch (%v)", path, err)
			continue
		}

		// Parse and merge
		var mainEntries, branchEntries []AuthorityEntry
		if err := json.Unmarshal([]byte(mainContent), &mainEntries); err != nil {
			log.Printf("[MergeCommit] Skipping %s: failed to parse main content (%v)", path, err)
			continue
		}
		if err := json.Unmarshal([]byte(branchContent), &branchEntries); err != nil {
			log.Printf("[MergeCommit] Skipping %s: failed to parse branch content (%v)", path, err)
			continue
		}

		log.Printf("[MergeCommit] %s: main has %d entries, branch has %d entries", path, len(mainEntries), len(branchEntries))

		// Merge: start with main, add unique entries from branch
		seenIDs := make(map[string]bool)
		var merged []AuthorityEntry
		for _, e := range mainEntries {
			seenIDs[e.ID] = true
			merged = append(merged, e)
		}
		newEntriesCount := 0
		for _, e := range branchEntries {
			if !seenIDs[e.ID] {
				merged = append(merged, e)
				newEntriesCount++
			}
		}

		mergedJSON, err := json.MarshalIndent(merged, "", "  ")
		if err != nil {
			log.Printf("[MergeCommit] Skipping %s: failed to marshal merged content (%v)", path, err)
			continue
		}

		treeEntries = append(treeEntries, &github.TreeEntry{
			Path:    github.String(path),
			Mode:    github.String("100644"),
			Type:    github.String("blob"),
			Content: github.String(string(mergedJSON)),
		})
		log.Printf("[MergeCommit] Merged %s: %d total entries (+%d new from branch)", path, len(merged), newEntriesCount)
	}

	// 3. Handle index.md files - regenerate based on combined articles
	log.Printf("[MergeCommit] Step 3: Regenerating index.md files...")
	// Get all markdown files from both branches
	allFiles := make(map[string]bool)
	for _, f := range mainFiles {
		allFiles[f] = true
	}
	for _, f := range branchFiles {
		allFiles[f] = true
	}
	log.Printf("[MergeCommit] Total unique files across both branches: %d", len(allFiles))

	// Group articles by directory
	articlesByDir := make(map[string][]string)
	indexDirs := make(map[string]bool)

	for file := range allFiles {
		if !strings.HasSuffix(file, ".md") || !strings.HasPrefix(file, "Compendium/") {
			continue
		}
		if strings.Contains(file, "_incoming") || strings.Contains(file, "_debug") {
			continue
		}

		lastSlash := strings.LastIndex(file, "/")
		if lastSlash == -1 {
			continue
		}
		dir := file[:lastSlash]
		filename := file[lastSlash+1:]

		if filename == "index.md" {
			indexDirs[dir] = true
		} else if !strings.HasPrefix(filename, "_") {
			articlesByDir[dir] = append(articlesByDir[dir], filename)
		}
	}

	// Generate merged index.md for each directory
	for dir := range indexDirs {
		articles := articlesByDir[dir]
		if len(articles) == 0 {
			continue
		}

		// Sort articles
		for i := 0; i < len(articles)-1; i++ {
			for j := i + 1; j < len(articles); j++ {
				if articles[i] > articles[j] {
					articles[i], articles[j] = articles[j], articles[i]
				}
			}
		}

		// Get category name
		parts := strings.Split(dir, "/")
		categoryName := parts[len(parts)-1]

		// Generate content
		var sb strings.Builder
		sb.WriteString("# ")
		sb.WriteString(categoryName)
		sb.WriteString(" Articles\n\n")

		for _, article := range articles {
			title := strings.TrimSuffix(article, ".md")
			title = strings.ReplaceAll(title, "-", " ")
			words := strings.Fields(title)
			for i, word := range words {
				if len(word) > 0 {
					words[i] = strings.ToUpper(word[:1]) + word[1:]
				}
			}
			title = strings.Join(words, " ")
			sb.WriteString("- [")
			sb.WriteString(title)
			sb.WriteString("](")
			sb.WriteString(article)
			sb.WriteString(")\n")
		}
		sb.WriteString("\n")

		indexPath := dir + "/index.md"
		treeEntries = append(treeEntries, &github.TreeEntry{
			Path:    github.String(indexPath),
			Mode:    github.String("100644"),
			Type:    github.String("blob"),
			Content: github.String(sb.String()),
		})
		log.Printf("Generated %s with %d articles", indexPath, len(articles))
	}

	log.Printf("[MergeCommit] Total tree entries to create: %d", len(treeEntries))

	if len(treeEntries) == 0 {
		return fmt.Errorf("no files to merge - this may indicate the branch is already up to date or has no new content")
	}

	// Create a new tree based on main's tree with our modifications
	log.Printf("[MergeCommit] Creating new tree based on main's tree (%s)...", baseTreeSHA[:7])
	newTree, _, err := c.client.Git.CreateTree(c.ctx, c.owner, c.repo, baseTreeSHA, treeEntries)
	if err != nil {
		return fmt.Errorf("failed to create tree: %w", err)
	}
	log.Printf("[MergeCommit] Created tree: %s", newTree.GetSHA()[:7])

	// Create a merge commit with two parents
	commitMessage := "Merge main into " + branch + " (resolved conflicts)"
	log.Printf("[MergeCommit] Creating merge commit with parents: main(%s), branch(%s)", mainSHA[:7], branchSHA[:7])
	newCommit, _, err := c.client.Git.CreateCommit(c.ctx, c.owner, c.repo, &github.Commit{
		Message: github.String(commitMessage),
		Tree:    &github.Tree{SHA: newTree.SHA},
		Parents: []*github.Commit{
			{SHA: github.String(mainSHA)},
			{SHA: github.String(branchSHA)},
		},
	}, nil)
	if err != nil {
		return fmt.Errorf("failed to create commit: %w", err)
	}
	log.Printf("[MergeCommit] Created merge commit: %s", newCommit.GetSHA()[:7])

	// Update the branch ref to point to the new commit
	log.Printf("[MergeCommit] Updating branch ref to point to new commit...")
	_, _, err = c.client.Git.UpdateRef(c.ctx, c.owner, c.repo, &github.Reference{
		Ref:    github.String("refs/heads/" + branch),
		Object: &github.GitObject{SHA: newCommit.SHA},
	}, false)
	if err != nil {
		return fmt.Errorf("failed to update branch ref: %w", err)
	}

	log.Printf("[MergeCommit] SUCCESS: Updated %s to %s", branch, newCommit.GetSHA()[:7])
	return nil
}

func (c *Client) ListOpenPRs() ([]*PRInfo, error) {
	if err := c.ensureValidToken(); err != nil {
		return nil, fmt.Errorf("failed to refresh token: %w", err)
	}

	opts := &github.PullRequestListOptions{
		State: "open",
	}
	prs, _, err := c.client.PullRequests.List(c.ctx, c.owner, c.repo, opts)
	if err != nil {
		return nil, err
	}

	var result []*PRInfo
	for _, pr := range prs {
		info := &PRInfo{
			Number: pr.GetNumber(),
			Title:  pr.GetTitle(),
			Body:   pr.GetBody(),
			Draft:  pr.GetDraft(),
		}

		// Get head branch name
		if pr.Head != nil {
			info.HeadBranch = pr.Head.GetRef()
		}

		// Extract issue references from body (e.g., "#123", "Closes #45")
		info.IssueRefs = extractIssueRefs(pr.GetBody())

		result = append(result, info)
	}
	return result, nil
}

// extractIssueRefs finds issue numbers referenced in text (e.g., #123, Closes #45)
func extractIssueRefs(text string) []int {
	var refs []int
	// Simple regex-free parsing for #NUMBER patterns
	for i := 0; i < len(text); i++ {
		if text[i] == '#' && i+1 < len(text) {
			// Extract the number after #
			j := i + 1
			for j < len(text) && text[j] >= '0' && text[j] <= '9' {
				j++
			}
			if j > i+1 {
				numStr := text[i+1 : j]
				if num, err := strconv.Atoi(numStr); err == nil && num > 0 {
					refs = append(refs, num)
				}
			}
		}
	}
	return refs
}

func (c *Client) ListFilesInBranch(branch, path string) ([]string, error) {
	tree, _, err := c.client.Git.GetTree(c.ctx, c.owner, c.repo, branch, true)
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

func (c *Client) DeleteFile(branch, path, message, sha string) error {
	opts := &github.RepositoryContentFileOptions{
		Message: github.String(message),
		SHA:     github.String(sha),
		Branch:  github.String(branch),
	}
	_, _, err := c.client.Repositories.DeleteFile(c.ctx, c.owner, c.repo, path, opts)
	return err
}

func (c *Client) MarkPRReady(prNumber int) error {
	// GitHub API doesn't have a direct "mark ready" endpoint
	// We need to use the GraphQL API or update the PR
	// For now, we'll use the REST API to update the draft status

	// The REST API doesn't support changing draft status directly
	// We need to use GraphQL mutation: markPullRequestReadyForReview

	query := `mutation($id: ID!) {
		markPullRequestReadyForReview(input: {pullRequestId: $id}) {
			pullRequest {
				isDraft
			}
		}
	}`

	// First, get the PR's node ID
	pr, _, err := c.client.PullRequests.Get(c.ctx, c.owner, c.repo, prNumber)
	if err != nil {
		return fmt.Errorf("failed to get PR: %w", err)
	}

	nodeID := pr.GetNodeID()

	// Execute GraphQL mutation
	var mutation struct {
		MarkPullRequestReadyForReview struct {
			PullRequest struct {
				IsDraft bool
			}
		} `graphql:"markPullRequestReadyForReview(input: {pullRequestId: $id})"`
	}

	variables := map[string]interface{}{
		"id": nodeID,
	}

	// Use raw HTTP request for GraphQL since go-github doesn't have native GraphQL support
	reqBody := struct {
		Query     string                 `json:"query"`
		Variables map[string]interface{} `json:"variables"`
	}{
		Query:     query,
		Variables: variables,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal GraphQL request: %w", err)
	}

	req, err := http.NewRequestWithContext(c.ctx, "POST", "https://api.github.com/graphql", strings.NewReader(string(jsonBody)))
	if err != nil {
		return fmt.Errorf("failed to create GraphQL request: %w", err)
	}

	// We need to get a token - extract from the client
	// This is a bit hacky, but go-github doesn't expose the token directly
	// We'll use the client's transport
	req.Header.Set("Content-Type", "application/json")

	// Use the client's underlying HTTP client which has auth
	resp, err := c.client.Client().Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute GraphQL request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GraphQL request failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Check for GraphQL errors
	var graphqlResp struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}

	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &graphqlResp); err == nil && len(graphqlResp.Errors) > 0 {
		return fmt.Errorf("GraphQL error: %s", graphqlResp.Errors[0].Message)
	}

	_ = mutation // Suppress unused variable warning

	return nil
}

func (c *Client) AddLabel(issueNumber int, label string) error {
	if err := c.ensureValidToken(); err != nil {
		return err
	}
	_, _, err := c.client.Issues.AddLabelsToIssue(c.ctx, c.owner, c.repo, issueNumber, []string{label})
	return err
}

func (c *Client) RemoveLabel(issueNumber int, label string) error {
	if err := c.ensureValidToken(); err != nil {
		return err
	}
	_, err := c.client.Issues.RemoveLabelForIssue(c.ctx, c.owner, c.repo, issueNumber, label)
	// GitHub returns 404 if label not found, we can ignore that
	if err != nil && !strings.Contains(err.Error(), "404") {
		return err
	}
	return nil
}

func (c *Client) HasLabel(issueNumber int, label string) (bool, error) {
	if err := c.ensureValidToken(); err != nil {
		return false, err
	}
	issue, _, err := c.client.Issues.Get(c.ctx, c.owner, c.repo, issueNumber)
	if err != nil {
		return false, err
	}
	for _, l := range issue.Labels {
		if l.GetName() == label {
			return true, nil
		}
	}
	return false, nil
}

// GetAuthenticatedUsername returns the username of the authenticated user/bot
func (c *Client) GetAuthenticatedUsername() (string, error) {
	if err := c.ensureValidToken(); err != nil {
		return "", err
	}
	user, _, err := c.client.Users.Get(c.ctx, "")
	if err != nil {
		return "", fmt.Errorf("failed to get authenticated user: %w", err)
	}
	return user.GetLogin(), nil
}

// GetIssue fetches a single issue by number
func (c *Client) GetIssue(issueNumber int) (*github.Issue, error) {
	if err := c.ensureValidToken(); err != nil {
		return nil, err
	}
	issue, _, err := c.client.Issues.Get(c.ctx, c.owner, c.repo, issueNumber)
	if err != nil {
		return nil, fmt.Errorf("failed to get issue #%d: %w", issueNumber, err)
	}
	return issue, nil
}

// AddAssignees adds assignees to an issue
func (c *Client) AddAssignees(issueNumber int, assignees []string) error {
	if err := c.ensureValidToken(); err != nil {
		return err
	}
	_, _, err := c.client.Issues.AddAssignees(c.ctx, c.owner, c.repo, issueNumber, assignees)
	if err != nil {
		return fmt.Errorf("failed to add assignees to issue #%d: %w", issueNumber, err)
	}
	return nil
}

// RemoveAssignees removes assignees from an issue
func (c *Client) RemoveAssignees(issueNumber int, assignees []string) error {
	if err := c.ensureValidToken(); err != nil {
		return err
	}
	_, _, err := c.client.Issues.RemoveAssignees(c.ctx, c.owner, c.repo, issueNumber, assignees)
	if err != nil {
		return fmt.Errorf("failed to remove assignees from issue #%d: %w", issueNumber, err)
	}
	return nil
}

func (c *Client) IsLocal() bool {
	return false
}
