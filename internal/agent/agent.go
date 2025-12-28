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

// LabelPendingReview is added to topic issues when a PR has been created for them
const LabelPendingReview = "pending review"

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

	repoMgr, err := repository.NewLocalGitManager(ctx, ghClient, repoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create local git manager: %w", err)
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

func (a *Agent) Run(ctx context.Context, stepByStep bool, stepName string) error {
	// Get bot username for assignment operations
	botUsername, err := a.gh.GetAuthenticatedUsername()
	if err != nil {
		slog.Warn("Failed to get authenticated username, skipping assignment locking", "error", err)
		botUsername = "" // Continue without locking
	} else {
		log.Printf("Bot identity: %s", botUsername)
	}

	// STEP 1: Cleanup - start with a clean slate
	log.Println("Performing cleanup before starting work...")
	if err := a.performCleanup(botUsername); err != nil {
		slog.Warn("Cleanup encountered issues", "error", err)
		// Continue anyway - cleanup is best-effort
	}

	// STEP 2: Check and merge any ready PRs (if enabled)
	if autoMerge := os.Getenv("AUTO_MERGE_READY_PRS"); autoMerge == "true" || autoMerge == "1" {
		if err := a.mergeReadyPRs(ctx); err != nil {
			slog.Warn("Error checking/merging PRs", "error", err)
		}
	} else {
		log.Println("Auto-merge disabled (AUTO_MERGE_READY_PRS=false), skipping PR merge check")
	}

	// STEP 3: Fetch topic issues and find available work
	log.Println("Checking for research topic issues...")
	topicIssues, err := a.gh.GetTopicIssues()
	if err != nil {
		return fmt.Errorf("failed to get topic issues: %w", err)
	}

	log.Printf("Found %d research topic issues", len(topicIssues))

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

	// Collect available topics - skip if any of these conditions are true:
	// 1. Has an open PR referencing it
	// 2. Has the "pending review" label (indicates PR was created for it)
	// 3. Is already assigned to someone
	var availableTopics []*gh.Issue
	for _, issue := range topicIssues {
		issueNum := *issue.Number

		// Skip if there's an open PR for this issue
		if issuesWithPRs[issueNum] {
			log.Printf("Skipping topic #%d: has open PR", issueNum)
			continue
		}

		// Skip if has "pending review" label (PR was created previously)
		hasLabel, err := a.gh.HasLabel(issueNum, LabelPendingReview)
		if err != nil {
			slog.Warn("Failed to check label", "issue", issueNum, "error", err)
		}
		if hasLabel {
			log.Printf("Skipping topic #%d: has '%s' label", issueNum, LabelPendingReview)
			continue
		}

		// Skip if already assigned
		if !isIssueUnassigned(issue) {
			log.Printf("Skipping topic #%d: already assigned", issueNum)
			continue
		}

		availableTopics = append(availableTopics, issue)
	}

	maxConcurrent := 1
	if v := os.Getenv("MAX_CONCURRENT_PRS"); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			maxConcurrent = i
		}
	}

	log.Printf("Status: Managed PRs: %d (Limit: %d), Available Topics: %d", len(managedPRs), maxConcurrent, len(availableTopics))

	// STEP 4: Try to claim and process a random topic
	if len(managedPRs) < maxConcurrent && len(availableTopics) > 0 {
		rand.Seed(time.Now().UnixNano())

		// Shuffle available topics to pick a random one
		rand.Shuffle(len(availableTopics), func(i, j int) {
			availableTopics[i], availableTopics[j] = availableTopics[j], availableTopics[i]
		})

		// Pick the first (now randomized) topic and try to claim it
		selectedTopic := availableTopics[0]
		issueNum := *selectedTopic.Number

		// If we have a bot username, try to claim with locking
		if botUsername != "" {
			claimed, err := a.claimIssue(issueNum, botUsername)
			if err != nil {
				slog.Warn("Error claiming issue", "issue", issueNum, "error", err)
				// Cleanup and signal caller to retry
				log.Println("Claim failed, performing cleanup for retry...")
				_ = a.performCleanup(botUsername)
				return fmt.Errorf("failed to claim issue #%d: %w", issueNum, err)
			}
			if !claimed {
				// Another instance got it - cleanup and signal caller to retry
				log.Println("Issue was claimed by another instance, performing cleanup for retry...")
				_ = a.performCleanup(botUsername)
				return fmt.Errorf("issue #%d was claimed by another instance", issueNum)
			}
		}

		// Successfully claimed, process the topic with iterations
		log.Printf("Selected topic issue #%d: %s", issueNum, selectedTopic.GetTitle())

		processErr := a.processTopicWithIterations(ctx, selectedTopic, botUsername)
		return processErr
	}

	if len(managedPRs) > 0 {
		rand.Seed(time.Now().UnixNano())
		pr := managedPRs[rand.Intn(len(managedPRs))]
		if stepByStep {
			return a.processExistingPRStepByStep(ctx, pr, stepName)
		}
		return a.processExistingPR(ctx, pr)
	}

	log.Println("No work to do (no available topics, no managed PRs to update)")
	return nil
}

// performCleanup resets the local repository to main and unassigns all issues from this bot.
// This ensures each run starts with a clean slate.
func (a *Agent) performCleanup(botUsername string) error {
	var errs []string

	// 1. Reset local branch to main
	currentBranch, err := a.gh.GetCurrentBranch()
	if err != nil {
		errs = append(errs, fmt.Sprintf("failed to get current branch: %v", err))
	} else if currentBranch != "main" {
		log.Printf("Resetting from branch '%s' to main...", currentBranch)
		if err := a.gh.ResetToMain(); err != nil {
			errs = append(errs, fmt.Sprintf("failed to reset to main: %v", err))
		} else {
			log.Println("Successfully reset to main branch")
		}
	}

	// 2. Unassign this bot from any issues it's currently assigned to
	if botUsername != "" {
		log.Println("Unassigning bot from any previously assigned issues...")
		issues, err := a.gh.GetTopicIssues()
		if err != nil {
			errs = append(errs, fmt.Sprintf("failed to get issues for cleanup: %v", err))
		} else {
			for _, issue := range issues {
				for _, assignee := range issue.Assignees {
					if assignee.GetLogin() == botUsername {
						log.Printf("Unassigning from issue #%d: %s", *issue.Number, *issue.Title)
						if err := a.gh.RemoveAssignees(*issue.Number, []string{botUsername}); err != nil {
							errs = append(errs, fmt.Sprintf("failed to unassign from issue #%d: %v", *issue.Number, err))
						}
						break
					}
				}
			}
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("cleanup errors: %s", strings.Join(errs, "; "))
	}
	return nil
}

func (a *Agent) mergeReadyPRs(ctx context.Context) error {
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
