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
		State:  "closed",
		Labels: []string{label},
		Sort:   "updated",
		Direction: "desc",
		ListOptions: github.ListOptions{
			PerPage: limit,
		},
	}
	issues, _, err := c.client.Issues.ListByRepo(c.ctx, c.owner, c.repo, opts)
	return issues, err
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

// ResolveAuthorityConflicts merges authority JSON files between main and PR branch.
// It fetches both versions, merges the arrays (deduping by ID), and updates the PR branch.
// It also handles category index.md files by regenerating them based on the PR branch content.
func (c *Client) ResolveAuthorityConflicts(headBranch string) error {
	if err := c.ensureValidToken(); err != nil {
		return fmt.Errorf("failed to refresh token: %w", err)
	}

	resolvedCount := 0

	// 1. Resolve authority JSON files
	authorityFiles := []string{
		"authority/topics.json",
		"authority/people.json",
		"authority/orgs.json",
		"authority/places.json",
	}

	for _, path := range authorityFiles {
		// Get file from main branch
		mainContent, _, err := c.GetFile("main", path)
		if err != nil {
			// File might not exist in main, skip
			log.Printf("Could not get %s from main: %v", path, err)
			continue
		}

		// Get file from head branch
		headContent, headSHA, err := c.GetFile(headBranch, path)
		if err != nil {
			// File might not exist in head, skip
			log.Printf("Could not get %s from %s: %v", path, headBranch, err)
			continue
		}

		// Parse both versions
		var mainEntries, headEntries []AuthorityEntry
		if err := json.Unmarshal([]byte(mainContent), &mainEntries); err != nil {
			log.Printf("Could not parse main %s: %v", path, err)
			continue
		}
		if err := json.Unmarshal([]byte(headContent), &headEntries); err != nil {
			log.Printf("Could not parse head %s: %v", path, err)
			continue
		}

		// Merge entries (main first, then add new entries from head)
		seenIDs := make(map[string]bool)
		var merged []AuthorityEntry

		// Add all entries from main
		for _, entry := range mainEntries {
			seenIDs[entry.ID] = true
			merged = append(merged, entry)
		}

		// Add entries from head that aren't in main
		newEntries := 0
		for _, entry := range headEntries {
			if !seenIDs[entry.ID] {
				seenIDs[entry.ID] = true
				merged = append(merged, entry)
				newEntries++
			}
		}

		// Skip if no new entries to add
		if newEntries == 0 {
			continue
		}

		// Marshal merged content
		mergedJSON, err := json.MarshalIndent(merged, "", "  ")
		if err != nil {
			log.Printf("Could not marshal merged %s: %v", path, err)
			continue
		}

		// Update file in head branch
		log.Printf("Merging %s: %d entries from main + %d new from head = %d total",
			path, len(mainEntries), newEntries, len(merged))
		if err := c.UpdateFile(headBranch, path, "Merge authority file: "+path, string(mergedJSON), headSHA); err != nil {
			return fmt.Errorf("failed to update %s: %w", path, err)
		}
		resolvedCount++
	}

	// 2. Resolve category index.md files by regenerating them
	// Get files from BOTH main AND the PR branch under Compendium/, then merge
	headFiles, err := c.ListFilesInBranch(headBranch, "Compendium")
	if err != nil {
		log.Printf("Could not list files in Compendium from %s: %v", headBranch, err)
		headFiles = []string{}
	}
	
	mainFiles, err := c.ListFilesInBranch("main", "Compendium")
	if err != nil {
		log.Printf("Could not list files in Compendium from main: %v", err)
		mainFiles = []string{}
	}
	
	// Combine files from both branches
	allFilesSet := make(map[string]bool)
	for _, file := range headFiles {
		allFilesSet[file] = true
	}
	for _, file := range mainFiles {
		allFilesSet[file] = true
	}
	
	// Group articles by directory (from combined set)
	articlesByDir := make(map[string][]string)
	indexFiles := make(map[string]bool)
	
	for file := range allFilesSet {
		if strings.HasSuffix(file, ".md") {
			if !strings.Contains(file, "/") {
				continue // Skip files not in a subdirectory
			}
			dir := file[:strings.LastIndex(file, "/")]
			filename := file[strings.LastIndex(file, "/")+1:]

			if filename == "index.md" {
				indexFiles[file] = true
			} else if !strings.HasPrefix(filename, "_") && !strings.Contains(dir, "_incoming") {
				// Regular article (not starting with _, not in _incoming)
				// Dedupe by checking if already added
				found := false
				for _, existing := range articlesByDir[dir] {
					if existing == filename {
						found = true
						break
					}
				}
				if !found {
					articlesByDir[dir] = append(articlesByDir[dir], filename)
				}
			}
		}
	}

	// For each index.md that exists in head branch, regenerate it based on combined articles
	for indexPath := range indexFiles {
		dir := indexPath[:strings.LastIndex(indexPath, "/")]
		articles := articlesByDir[dir]

		if len(articles) == 0 {
			continue
		}
		
		// Check if this index.md exists in the head branch and get its SHA
		_, indexSHA, err := c.GetFile(headBranch, indexPath)
		if err != nil {
			// index.md doesn't exist in head branch, skip
			continue
		}

		// Get the category name from the directory path (last component)
		parts := strings.Split(dir, "/")
		categoryName := parts[len(parts)-1]

		// Generate index content
		var sb strings.Builder
		sb.WriteString("# ")
		sb.WriteString(categoryName)
		sb.WriteString(" Articles\n\n")

		// Sort articles for consistent output
		sortedArticles := make([]string, len(articles))
		copy(sortedArticles, articles)
		for i := 0; i < len(sortedArticles)-1; i++ {
			for j := i + 1; j < len(sortedArticles); j++ {
				if sortedArticles[i] > sortedArticles[j] {
					sortedArticles[i], sortedArticles[j] = sortedArticles[j], sortedArticles[i]
				}
			}
		}

		for _, article := range sortedArticles {
			// Convert filename to title (remove .md, convert dashes to spaces, title case)
			title := strings.TrimSuffix(article, ".md")
			title = strings.ReplaceAll(title, "-", " ")
			// Simple title case
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

		// Update the index file
		log.Printf("Regenerating %s with %d articles", indexPath, len(articles))
		if err := c.UpdateFile(headBranch, indexPath, "Regenerate category index: "+indexPath, sb.String(), indexSHA); err != nil {
			log.Printf("Failed to update %s: %v", indexPath, err)
			continue
		}
		resolvedCount++
	}

	if resolvedCount == 0 {
		return fmt.Errorf("no files needed merging")
	}

	log.Printf("Resolved conflicts in %d files", resolvedCount)
	
	// After updating files, try to merge main into the PR branch again
	// This creates a proper merge commit with the resolved content
	log.Printf("Attempting to create merge commit for branch %s...", headBranch)
	if err := c.MergeMainIntoBranch(headBranch); err != nil {
		log.Printf("Could not create merge commit: %v (this is expected if files still differ)", err)
	} else {
		log.Printf("Successfully merged main into %s", headBranch)
	}
	
	return nil
}

// MergeMainIntoBranch creates a merge commit that merges main into the specified branch.
// This properly resolves the git history so the PR becomes mergeable.
func (c *Client) MergeMainIntoBranch(branch string) error {
	if err := c.ensureValidToken(); err != nil {
		return fmt.Errorf("failed to refresh token: %w", err)
	}
	
	// Get the SHA of main's HEAD
	mainRef, _, err := c.client.Git.GetRef(c.ctx, c.owner, c.repo, "heads/main")
	if err != nil {
		return fmt.Errorf("failed to get main ref: %w", err)
	}
	mainSHA := mainRef.Object.GetSHA()
	
	// Get the SHA of the branch's HEAD
	branchRef, _, err := c.client.Git.GetRef(c.ctx, c.owner, c.repo, "heads/"+branch)
	if err != nil {
		return fmt.Errorf("failed to get branch ref: %w", err)
	}
	branchSHA := branchRef.Object.GetSHA()
	
	// Try to create a merge using the repos merge API
	// This is like running "git merge main" in the PR branch
	mergeReq := &github.RepositoryMergeRequest{
		Base:          github.String(branch),
		Head:          github.String("main"),
		CommitMessage: github.String("Merge main into " + branch),
	}
	
	_, resp, err := c.client.Repositories.Merge(c.ctx, c.owner, c.repo, mergeReq)
	if err != nil {
		// Check if it's a conflict error (409)
		if resp != nil && resp.StatusCode == 409 {
			return fmt.Errorf("merge conflict: branches cannot be automatically merged (main=%s, branch=%s)", mainSHA[:7], branchSHA[:7])
		}
		// Check if already merged (204 No Content or similar)
		if resp != nil && resp.StatusCode == 204 {
			log.Printf("Branch %s is already up to date with main", branch)
			return nil
		}
		return fmt.Errorf("merge failed: %w", err)
	}
	
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
