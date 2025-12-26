package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"math/rand"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gitopedia/researcher/internal/authority"
	"github.com/gitopedia/researcher/internal/github"
	"github.com/gitopedia/researcher/internal/llm"
	"github.com/gitopedia/researcher/internal/repository"
	"github.com/gitopedia/researcher/internal/search"
	gh "github.com/google/go-github/v57/github"
	"github.com/oklog/ulid/v2"
)

var Version = "dev"

func init() {
	if Version == "dev" {
		if data, err := os.ReadFile("VERSION"); err == nil {
			Version = strings.TrimSpace(string(data))
		}
	}
}

type Agent struct {
	gh     repository.RepoManager
	search search.Searcher
	llm    llm.Generator
}

func debugBasePath(slug string) string {
	return fmt.Sprintf("Compendium/_debug/articles/%s", slug)
}

func (a *Agent) SetNoCommit(val bool) {
	a.gh.SetNoCommit(val)
}

func (a *Agent) saveDebugJSON(branchName, path, message string, v interface{}) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		slog.Warn("Failed to marshal debug JSON", "path", path, "error", err)
		return
	}
	if err := a.gh.CreateFile(branchName, path, message, string(data)); err != nil {
		slog.Warn("Failed to save debug JSON", "path", path, "error", err)
	}
}

func (a *Agent) saveDebugText(branchName, path, message, content string) {
	if err := a.gh.CreateFile(branchName, path, message, content); err != nil {
		slog.Warn("Failed to save debug text", "path", path, "error", err)
	}
}

func NewAgent(ctx context.Context, repoPath string) (*Agent, error) {
	ghClient, err := github.NewClient(ctx)
	if err != nil {
		return nil, err
	}

	var repoMgr repository.RepoManager
	if repoPath != "" {
		repoMgr, err = repository.NewLocalGitManager(ctx, ghClient, repoPath)
		if err != nil {
			return nil, fmt.Errorf("failed to create local git manager: %w", err)
		}
	} else {
		// Use a simple wrapper or cast if GitHubClient is compatible
		repoMgr = &remoteRepoManager{ghClient}
	}

	llmClient, err := llm.NewClient()
	if err != nil {
		return nil, fmt.Errorf("failed to create LLM client: %w", err)
	}
	return &Agent{
		gh:     repoMgr,
		search: search.NewClient(),
		llm:    llmClient,
	}, nil
}

type remoteRepoManager struct {
	github.GitHubClient
}

func (r *remoteRepoManager) GetRepoPath() string { return "" }
func (r *remoteRepoManager) IsLocal() bool      { return false }
func (r *remoteRepoManager) SetNoCommit(bool)   {}

func NewAgentWithDeps(gh repository.RepoManager, s search.Searcher, l llm.Generator) *Agent {
	return &Agent{
		gh:     gh,
		search: s,
		llm:    l,
	}
}

func (a *Agent) MergeOnly(ctx context.Context) error {
	log.Println("Running merge-only mode...")
	return a.mergeReadyPRs(ctx)
}

// cleanupStaleAssignments removes this bot from any issues it's still assigned to
// from previous interrupted runs
func (a *Agent) cleanupStaleAssignments(issues []*gh.Issue, botUsername string) {
	for _, issue := range issues {
		for _, assignee := range issue.Assignees {
			if assignee.GetLogin() == botUsername {
				log.Printf("Cleaning up stale assignment on issue #%d: %s", *issue.Number, *issue.Title)
				if err := a.gh.RemoveAssignees(*issue.Number, []string{botUsername}); err != nil {
					slog.Warn("Failed to remove stale assignment", "issue", *issue.Number, "error", err)
				}
				break
			}
		}
	}
}

// claimIssue attempts to claim an issue for processing using assignment-based locking.
// Returns true if this instance successfully claimed the issue, false if another instance claimed it.
func (a *Agent) claimIssue(issueNumber int, botUsername string) (bool, error) {
	// Assign ourselves to the issue
	if err := a.gh.AddAssignees(issueNumber, []string{botUsername}); err != nil {
		return false, fmt.Errorf("failed to assign issue #%d: %w", issueNumber, err)
	}

	// Wait a moment to allow race conditions to manifest
	claimWait := 2 * time.Second
	if v := os.Getenv("CLAIM_WAIT_SECONDS"); v != "" {
		if i, err := strconv.Atoi(v); err == nil && i > 0 {
			claimWait = time.Duration(i) * time.Second
		}
	}
	log.Printf("Waiting %v to verify sole ownership of issue #%d...", claimWait, issueNumber)
	time.Sleep(claimWait)

	// Re-fetch the issue to check assignees
	issue, err := a.gh.GetIssue(issueNumber)
	if err != nil {
		// If we can't verify, unassign and fail
		_ = a.gh.RemoveAssignees(issueNumber, []string{botUsername})
		return false, fmt.Errorf("failed to verify assignment on issue #%d: %w", issueNumber, err)
	}

	// Check if we're the sole assignee
	if len(issue.Assignees) == 1 && issue.Assignees[0].GetLogin() == botUsername {
		log.Printf("Successfully claimed issue #%d", issueNumber)
		return true, nil
	}

	// Someone else also assigned themselves - back off
	log.Printf("Issue #%d has multiple assignees or different assignee, backing off", issueNumber)
	if err := a.gh.RemoveAssignees(issueNumber, []string{botUsername}); err != nil {
		slog.Warn("Failed to unassign after claim conflict", "issue", issueNumber, "error", err)
	}
	return false, nil
}

// isIssueUnassigned checks if an issue has no assignees
func isIssueUnassigned(issue *gh.Issue) bool {
	return len(issue.Assignees) == 0
}

// ArticleCandidate represents an unchecked article from a topic issue
type ArticleCandidate struct {
	ArticleName   string
	TopicIssue    *gh.Issue
	TopicIssueNum int
}

func (a *Agent) Run(ctx context.Context, stepByStep bool, stepName string) error {
	if err := a.mergeReadyPRs(ctx); err != nil {
		slog.Warn("Error checking/merging PRs", "error", err)
	}

	// Get bot username for assignment operations
	botUsername, err := a.gh.GetAuthenticatedUsername()
	if err != nil {
		slog.Warn("Failed to get authenticated username, skipping assignment locking", "error", err)
		botUsername = "" // Continue without locking
	} else {
		log.Printf("Bot identity: %s", botUsername)
	}

	log.Println("Checking for research topic issues...")
	topicIssues, err := a.gh.GetTopicIssues()
	if err != nil {
		return fmt.Errorf("failed to get topic issues: %w", err)
	}

	log.Printf("Found %d research topic issues", len(topicIssues))

	// Cleanup: unassign from any old issues we're still assigned to
	if botUsername != "" {
		a.cleanupStaleAssignments(topicIssues, botUsername)
	}

	openPRs, err := a.gh.ListOpenPRs()
	if err != nil {
		slog.Warn("Failed to list open PRs", "error", err)
		openPRs = nil
	}

	issuesWithPRs := make(map[int]bool)
	var managedPRs []*github.PRInfo

	for _, pr := range openPRs {
		status, err := a.gh.GetPRStatus(pr.Number)
		if err != nil {
			slog.Warn("Failed to get PR status", "pr", pr.Number, "error", err)
			continue
		}
		if status.State == "open" && !status.Merged {
			for _, issueNum := range pr.IssueRefs {
				issuesWithPRs[issueNum] = true
			}
			if strings.HasPrefix(pr.HeadBranch, "research/") || strings.HasPrefix(pr.HeadBranch, "expand/") {
				managedPRs = append(managedPRs, pr)
			}
		}
	}

	// Collect all unchecked articles from topic issues
	var availableArticles []ArticleCandidate
	for _, issue := range topicIssues {
		if issuesWithPRs[*issue.Number] {
			continue // Skip issues that already have PRs
		}
		if !isIssueUnassigned(issue) {
			continue // Skip issues that are already assigned
		}

		// Parse articles from issue body
		body := issue.GetBody()
		articles := github.ParseArticlesFromBody(body)

		for _, article := range articles {
			if !article.Completed {
				availableArticles = append(availableArticles, ArticleCandidate{
					ArticleName:   article.Name,
					TopicIssue:    issue,
					TopicIssueNum: *issue.Number,
				})
			}
		}
	}

	maxConcurrent := 1
	if v := os.Getenv("MAX_CONCURRENT_PRS"); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			maxConcurrent = i
		}
	}

	log.Printf("Status: Managed PRs: %d (Limit: %d), Available Articles: %d", len(managedPRs), maxConcurrent, len(availableArticles))

	if len(managedPRs) < maxConcurrent && len(availableArticles) > 0 {
		rand.Seed(time.Now().UnixNano())

		// Shuffle available articles to avoid all instances trying the same one
		rand.Shuffle(len(availableArticles), func(i, j int) {
			availableArticles[i], availableArticles[j] = availableArticles[j], availableArticles[i]
		})

		// Try to claim an article's parent topic issue
		for _, candidate := range availableArticles {
			issueNum := candidate.TopicIssueNum

			// If we have a bot username, try to claim with locking
			if botUsername != "" {
				claimed, err := a.claimIssue(issueNum, botUsername)
				if err != nil {
					slog.Warn("Error claiming issue", "issue", issueNum, "error", err)
					continue
				}
				if !claimed {
					// Another instance got it, try next article
					continue
				}
			}

			// Successfully claimed (or no locking), process the article
			log.Printf("Selected article '%s' from topic issue #%d: %s",
				candidate.ArticleName, candidate.TopicIssueNum, candidate.TopicIssue.GetTitle())

			// Create a synthetic issue with the article name as title for processNewTopic
			syntheticIssue := &gh.Issue{
				Number: gh.Int(candidate.TopicIssueNum),
				Title:  gh.String(candidate.ArticleName),
			}

			var processErr error
			if stepByStep {
				processErr = a.processNewTopicStepByStep(ctx, syntheticIssue, stepName)
			} else {
				processErr = a.processNewTopic(ctx, syntheticIssue)
			}

			// After successful processing, check off the article in the topic issue
			if processErr == nil {
				log.Printf("Research complete, checking off article '%s' in issue #%d", candidate.ArticleName, candidate.TopicIssueNum)
				// Re-fetch the issue to get the latest body (in case it was modified)
				latestIssue, err := a.gh.GetIssue(candidate.TopicIssueNum)
				if err != nil {
					slog.Warn("Failed to re-fetch issue for checkbox update", "issue", candidate.TopicIssueNum, "error", err)
				} else {
					newBody := github.CheckArticleInBody(latestIssue.GetBody(), candidate.ArticleName)
					if err := a.gh.UpdateIssueBody(candidate.TopicIssueNum, newBody); err != nil {
						slog.Warn("Failed to update issue body with checked article", "issue", candidate.TopicIssueNum, "error", err)
					} else {
						log.Printf("Successfully checked off article '%s'", candidate.ArticleName)
					}
				}

				// Unassign from the issue after completion
				if botUsername != "" {
					if err := a.gh.RemoveAssignees(candidate.TopicIssueNum, []string{botUsername}); err != nil {
						slog.Warn("Failed to unassign after completion", "issue", candidate.TopicIssueNum, "error", err)
					}
				}
			}

			return processErr
		}

		// All claim attempts failed, no work available
		log.Println("All available articles were claimed by other instances")
	}

	if len(managedPRs) > 0 {
		rand.Seed(time.Now().UnixNano())
		pr := managedPRs[rand.Intn(len(managedPRs))]
		if stepByStep {
			return a.processExistingPRStepByStep(ctx, pr, stepName)
		}
		return a.processExistingPR(ctx, pr)
	}

	log.Println("No work to do (no available articles in topic issues, no managed PRs to update)")
	return nil
}

func (a *Agent) mergeReadyPRs(ctx context.Context) error {
	if a.gh.IsLocal() {
		// Skip merge in local mode
		return nil
	}
	log.Println("Checking for PRs ready to merge...")
	openPRs, err := a.gh.ListOpenPRs()
	if err != nil {
		return fmt.Errorf("failed to list open PRs: %w", err)
	}

	if len(openPRs) == 0 {
		log.Println("No open PRs found.")
		return nil
	}

	log.Printf("Found %d open PRs, checking status...", len(openPRs))
	mergedCount := 0

	for _, pr := range openPRs {
		status, err := a.gh.GetPRStatus(pr.Number)
		if err != nil {
			slog.Warn("Failed to get status for PR", "pr", pr.Number, "error", err)
			continue
		}

		if !status.Draft && status.CIStatus == "success" && status.Mergeable != nil && *status.Mergeable {
			log.Printf("PR #%d is ready to merge!", pr.Number)
			commitMsg := fmt.Sprintf("Merge PR #%d: automated content expansion", pr.Number)
			if err := a.gh.MergePR(pr.Number, commitMsg); err != nil {
				slog.Error("Failed to merge PR", "pr", pr.Number, "error", err)
			} else {
				log.Printf("Successfully merged PR #%d", pr.Number)
				mergedCount++
			}
		}
	}
	return nil
}

// saveSourceSummary saves a source summary to the repository
// Used by incremental.go
func (a *Agent) saveSourceSummary(ctx context.Context, src SourceInfo, topic, slug, branchName string, authMgr *authority.Manager, debugSources bool) error {
	u, err := url.Parse(src.URL)
	if err != nil {
		return err
	}

	domain := strings.ReplaceAll(u.Host, ".", "-")
	sourceID := ulid.Make().String()
	sourcePath := fmt.Sprintf("Compendium/_incoming/sources/%s--%s-%d.md", slug, domain, src.Index)

	sourceEntities, err := a.llm.ExtractEntities(ctx, src.Summary)
	if err != nil {
		slog.Warn("Entity extraction failed for source", "error", err)
		sourceEntities = nil
	}
	sourceEntities = append(sourceEntities, llm.ExtractedEntity{Name: topic, Type: llm.Topic})

	resolvedSource, err := authMgr.ResolveEntities(sourceEntities)
	if err != nil {
		resolvedSource = make(map[string][]string)
	}

	var sourceTags []string
	if topicIDs, ok := resolvedSource["topic"]; ok {
		sourceTags = topicIDs
	}
	sourceTagsStr := fmt.Sprintf("[\"%s\"]", strings.Join(sourceTags, "\", \""))

	sourceFacets := ""
	if ids, ok := resolvedSource["person"]; ok && len(ids) > 0 {
		sourceFacets += fmt.Sprintf("people: [\"%s\"]\n", strings.Join(ids, "\", \""))
	}
	if ids, ok := resolvedSource["org"]; ok && len(ids) > 0 {
		sourceFacets += fmt.Sprintf("orgs: [\"%s\"]\n", strings.Join(ids, "\", \""))
	}
	if ids, ok := resolvedSource["place"]; ok && len(ids) > 0 {
		sourceFacets += fmt.Sprintf("places: [\"%s\"]\n", strings.Join(ids, "\", \""))
	}

	sourceContent := fmt.Sprintf(`---
id: %s
slug: "%s--%s-%d"
title: "Source: %s"
url: "%s"
type: source
related_article: "%s"
created: %s
tags: %s
%ssummary: "Summarized source material for %s"
researcher_version: "%s"
---

%s
`, sourceID, slug, domain, src.Index, src.Title, src.URL, slug,
		time.Now().UTC().Format("2006-01-02T15:04:05Z"), sourceTagsStr, sourceFacets, topic, Version, src.Summary)

	return a.gh.CreateFile(branchName, sourcePath, "Add source: "+src.Title, sourceContent)
}

// stripCodeFences removes markdown code fences that wrap YAML frontmatter
func stripCodeFences(content string) string {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "```") {
		return content
	}
	firstNewline := strings.Index(content, "\n")
	if firstNewline == -1 {
		return content
	}
	content = content[firstNewline+1:]
	lastFence := strings.LastIndex(content, "```")
	if lastFence != -1 {
		content = content[:lastFence]
	}
	return strings.TrimSpace(content)
}
