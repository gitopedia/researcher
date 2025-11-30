package agent

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"math/rand"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gitopedia/researcher/internal/authority"
	"github.com/gitopedia/researcher/internal/github"
	"github.com/gitopedia/researcher/internal/llm"
	"github.com/gitopedia/researcher/internal/search"
	gh "github.com/google/go-github/v57/github"
	"github.com/oklog/ulid/v2"
)

type Agent struct {
	gh     github.GitHubClient
	search search.Searcher
	llm    llm.Generator
}

func NewAgent(ctx context.Context) (*Agent, error) {
	ghClient, err := github.NewClient(ctx)
	if err != nil {
		return nil, err
	}

	llmClient, err := llm.NewClient()
	if err != nil {
		return nil, fmt.Errorf("failed to create LLM client: %w", err)
	}

	return &Agent{
		gh:     ghClient,
		search: search.NewClient(),
		llm:    llmClient,
	}, nil
}

func NewAgentWithDeps(gh github.GitHubClient, s search.Searcher, l llm.Generator) *Agent {
	return &Agent{
		gh:     gh,
		search: s,
		llm:    l,
	}
}

func (a *Agent) Run(ctx context.Context) error {
	// First, check if any PRs are ready to merge
	if err := a.mergeReadyPRs(ctx); err != nil {
		slog.Warn("Error checking/merging PRs", "error", err)
	}

	log.Println("Checking for research category issues...")
	issues, err := a.gh.GetResearchRequests()
	if err != nil {
		return fmt.Errorf("failed to get issues: %w", err)
	}

	if len(issues) == 0 {
		log.Println("No research category issues found.")
		return nil
	}

	log.Printf("Found %d research category issues:", len(issues))
	for _, issue := range issues {
		log.Printf("  - Issue #%d: %s", *issue.Number, *issue.Title)
	}

	// Get open PRs to filter out issues that already have PRs
	openPRs, err := a.gh.ListOpenPRs()
	if err != nil {
		slog.Warn("Failed to list open PRs", "error", err)
		openPRs = nil
	}

	// Build set of issue numbers that have open (non-merged) PRs
	issuesWithPRs := make(map[int]bool)
	for _, pr := range openPRs {
		// Check if PR is actually merged or closed
		status, err := a.gh.GetPRStatus(pr.Number)
		if err != nil {
			slog.Warn("Failed to get PR status when checking for open PRs", "pr", pr.Number, "error", err)
			// If we can't check, assume it's open to be safe
			for _, issueNum := range pr.IssueRefs {
				issuesWithPRs[issueNum] = true
				log.Printf("Issue #%d has PR #%d (status unknown), will skip", issueNum, pr.Number)
			}
			continue
		}

		// Only skip issues if PR is open and not merged
		if status.State == "open" && !status.Merged {
			for _, issueNum := range pr.IssueRefs {
				issuesWithPRs[issueNum] = true
				log.Printf("Issue #%d has open PR #%d, will skip", issueNum, pr.Number)
			}
		} else if status.Merged {
			for _, issueNum := range pr.IssueRefs {
				log.Printf("Issue #%d has merged PR #%d, will process", issueNum, pr.Number)
			}
		} else if status.State == "closed" {
			for _, issueNum := range pr.IssueRefs {
				log.Printf("Issue #%d has closed PR #%d, will process", issueNum, pr.Number)
			}
		}
	}

	// Filter issues to only those without open PRs
	var availableIssues []*gh.Issue
	for _, issue := range issues {
		if !issuesWithPRs[*issue.Number] {
			availableIssues = append(availableIssues, issue)
		}
	}

	if len(availableIssues) == 0 {
		return fmt.Errorf("all research issues already have open PRs - no work available")
	}

	log.Printf("Found %d issues without PRs (out of %d total)", len(availableIssues), len(issues))

	// Pick one random issue from available ones
	rand.Seed(time.Now().UnixNano())
	issue := availableIssues[rand.Intn(len(availableIssues))]

	// Process the research task
	if err := a.expandCategory(ctx, issue); err != nil {
		return err
	}

	// After completing research, check again for ready PRs to merge
	if err := a.mergeReadyPRs(ctx); err != nil {
		slog.Warn("Error checking/merging PRs after research", "error", err)
	}

	return nil
}

func (a *Agent) expandCategory(ctx context.Context, issue *gh.Issue) error {
	log.Printf("Expanding Category: %s (Issue #%d)", *issue.Title, *issue.Number)

	// Parse category name: "Category: [Name]"
	title := *issue.Title
	category := strings.TrimSpace(strings.TrimPrefix(title, "Category:"))
	if category == title {
		// Fallback if format is different
		category = title
	}

	// 1. Context: List existing articles
	log.Println("Listing existing articles...")
	files, err := a.gh.ListAllFiles("Compendium")
	if err != nil {
		return fmt.Errorf("failed to list files: %w", err)
	}
	var existingTitles []string
	for _, f := range files {
		if strings.HasSuffix(f, ".md") && !strings.HasSuffix(f, "index.md") {
			// Convert filename to title roughly (slug to title logic is fuzzy but good enough for LLM context)
			// e.g. Compendium/Technology/AI/OpenAI.md -> OpenAI
			base := filepath.Base(f)
			name := strings.TrimSuffix(base, filepath.Ext(base))
			existingTitles = append(existingTitles, name)
		}
	}
	log.Printf("Found %d existing articles.", len(existingTitles))

	// 2. Discovery
	log.Printf("Asking LLM for missing topics in '%s'...", category)
	candidates, err := a.llm.SuggestTopics(ctx, category, existingTitles)
	if err != nil {
		return fmt.Errorf("topic suggestion failed: %w", err)
	}
	log.Printf("LLM suggested %d topics.", len(candidates))

	// Determine max topics to process
	maxTopics := 10
	if envVal := os.Getenv("MAX_TOPICS_PER_RUN"); envVal != "" {
		if val, err := strconv.Atoi(envVal); err == nil && val > 0 {
			maxTopics = val
		}
	}

	// Select top N
	if len(candidates) > maxTopics {
		candidates = candidates[:maxTopics]
	}
	log.Printf("Selected topics (limited to %d): %v", maxTopics, candidates)

	if len(candidates) == 0 {
		log.Println("No new topics suggested.")
		return nil
	}

	// 3. Branching
	timestamp := time.Now().Format("20060102-150405")
	sanitizedCat := strings.ReplaceAll(strings.ToLower(category), " ", "-")
	branchName := fmt.Sprintf("expand/%s-%s", sanitizedCat, timestamp)
	log.Printf("Creating branch %s...", branchName)
	if err := a.gh.CreateBranch("main", branchName); err != nil {
		return fmt.Errorf("failed to create branch: %w", err)
	}

	// 4. Load Authorities
	authMgr := authority.NewManager(a.gh)
	if err := authMgr.Load("main"); err != nil {
		slog.Warn("Failed to load authorities", "error", err)
	}

	// 5. Generation Loop
	// Create progress tracker
	progress := NewProgressTracker()
	defer progress.Finish()
	progress.SetPhase(PhaseInitialGathering)

	var createdArticles []string
	for _, topic := range candidates {
		// Check for cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		progress.SetTopic(topic)

		log.Printf("Processing topic: %s", topic)
		if err := a.processTopic(ctx, topic, category, branchName, authMgr, progress); err != nil {
			if err == context.Canceled {
				return err
			}
			slog.Error("Error processing topic", "topic", topic, "error", err)
			continue
		}
		createdArticles = append(createdArticles, topic)

		// Phase 2: Detailed Information Gathering (placeholder)
		progress.SetPhase(PhaseDetailedGathering)
		log.Printf("Phase 2: Detailed Information Gathering (placeholder - not yet implemented)")
		// TODO: Implement phase 2 - gather sources for subtopics
		progress.SetPhase(PhaseInitialGathering) // Reset for next topic
	}

	if len(createdArticles) == 0 {
		return fmt.Errorf("failed to generate any articles")
	}

	// 6. Commit Authority Updates
	updates, err := authMgr.GetUpdates()
	if err != nil {
		slog.Error("Failed to get authority updates", "error", err)
	} else {
		for path, update := range updates {
			log.Printf("Updating authority file: %s", path)
			if update.SHA == "" {
				if err := a.gh.CreateFile(branchName, path, "Create authority "+path, update.Content); err != nil {
					slog.Error("Failed to create authority file", "path", path, "error", err)
				}
			} else {
				if err := a.gh.UpdateFile(branchName, path, "Update authority "+path, update.Content, update.SHA); err != nil {
					slog.Error("Failed to update authority file", "path", path, "error", err)
				}
			}
		}
	}

	// 7. Create PR
	prTitle := fmt.Sprintf("Expand %s: %s", category, strings.Join(createdArticles, ", "))
	if len(prTitle) > 200 {
		prTitle = fmt.Sprintf("Expand %s: %d new articles", category, len(createdArticles))
	}

	prBody := fmt.Sprintf("Automated expansion for category **%s**.\n\nAdded articles:\n", category)
	for _, art := range createdArticles {
		prBody += fmt.Sprintf("- %s\n", art)
	}
	prBody += fmt.Sprintf("\nTracking Issue: #%d", *issue.Number)

	pr, err := a.gh.CreatePullRequest(prTitle, prBody, branchName, "main")
	if err != nil {
		return fmt.Errorf("failed to create PR: %w", err)
	}
	log.Printf("Created PR #%d", *pr.Number)

	// 8. Comment on Issue
	comment := fmt.Sprintf("Expanded category with %d articles: %s. PR: %s", len(createdArticles), strings.Join(createdArticles, ", "), *pr.HTMLURL)
	if err := a.gh.CommentOnIssue(*issue.Number, comment); err != nil {
		slog.Warn("Failed to comment on issue", "issue", *issue.Number, "error", err)
	}

	// 9. Organize articles (move from _incoming to proper Compendium paths)
	if err := a.organizeArticles(ctx, branchName, *pr.Number); err != nil {
		slog.Error("Failed to organize articles", "error", err)
		// Add a comment about the failure
		comment := fmt.Sprintf("⚠️ Article organization failed: %v\n\nPlease organize articles manually.", err)
		if commentErr := a.gh.CommentOnPR(*pr.Number, comment); commentErr != nil {
			slog.Warn("Failed to add failure comment", "error", commentErr)
		}
	}

	log.Printf("Research task complete. PR #%d created and articles organized.", *pr.Number)
	return nil
}

// mergeReadyPRs checks all open PRs and merges any that are ready
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

	// Helper function to check and attempt merge
	tryMerge := func(status *github.PRStatus, pr *github.PRInfo) bool {
		mergeableStr := "unknown"
		if status.Mergeable != nil {
			if *status.Mergeable {
				mergeableStr = "true"
			} else {
				mergeableStr = "false"
			}
		}
		log.Printf("PR #%d: draft=%v, state=%s, ci=%s, mergeable=%s",
			pr.Number, status.Draft, status.State, status.CIStatus, mergeableStr)

		// Check if ready to merge: not draft, CI passed, and mergeable is explicitly true
		// We no longer treat mergeable=nil (unknown) as mergeable - it often means GitHub
		// is still calculating after new commits were pushed, and attempting to merge
		// will fail with 405 "Pull Request is not mergeable"
		if !status.Draft && status.CIStatus == "success" && status.Mergeable != nil && *status.Mergeable {
			log.Printf("PR #%d is ready to merge!", pr.Number)

			commitMsg := fmt.Sprintf("Merge PR #%d: automated content expansion", pr.Number)
			if err := a.gh.MergePR(pr.Number, commitMsg); err != nil {
				slog.Error("Failed to merge PR", "pr", pr.Number, "error", err)
				return false
			}
			log.Printf("Successfully merged PR #%d", pr.Number)
			mergedCount++
			// Note: We don't manually close issues here.
			// GitHub automatically closes issues when PRs with "Closes #X" or "Fixes #X" are merged.
			return true
		}
		return false
	}
	for _, pr := range openPRs {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		status, err := a.gh.GetPRStatus(pr.Number)
		if err != nil {
			slog.Warn("Failed to get status for PR", "pr", pr.Number, "error", err)
			continue
		}

		// Try to merge if ready
		if tryMerge(status, pr) {
			continue // Successfully merged, move to next PR
		}

		// Handle different PR states
		if status.CIStatus == "failure" {
			log.Printf("PR #%d has failed CI - needs manual attention", pr.Number)
		} else if status.Draft {
			log.Printf("PR #%d is still a draft - waiting for Encyclopaedist", pr.Number)
		} else if status.CIStatus == "pending" {
			log.Printf("PR #%d CI is still running", pr.Number)
		} else if status.Mergeable == nil {
			// Mergeable status is unknown - GitHub is still calculating (often after new commits)
			log.Printf("PR #%d mergeable status unknown - GitHub is calculating, will check on next run", pr.Number)
		} else if !*status.Mergeable {
			// PR has conflicts - try to update the branch by merging main into it
			log.Printf("PR #%d has merge conflicts - attempting to update branch from main...", pr.Number)
			if err := a.gh.UpdatePRBranch(pr.Number); err != nil {
				// GitHub's UpdateBranch failed - likely due to actual file conflicts
				// Try to resolve authority file conflicts programmatically
				log.Printf("PR #%d: GitHub merge failed, attempting to resolve authority file conflicts...", pr.Number)
				if pr.HeadBranch == "" {
					slog.Error("Cannot resolve conflicts: PR has no head branch info", "pr", pr.Number)
				} else {
					resolveErr := a.gh.ResolveAuthorityConflicts(pr.HeadBranch)
					if resolveErr != nil {
						// Check if error indicates we've already tried (no files needed merging)
						if strings.Contains(resolveErr.Error(), "no files needed merging") {
							// We've already resolved the files but PR is still unmergeable.
							// This means the conflict is in the git history, not file contents.
							// Close the PR so the issue can spawn a new PR from current main.
							log.Printf("PR #%d: authority files already merged but PR still has conflicts - closing PR to allow fresh start", pr.Number)
							if closeErr := a.gh.ClosePR(pr.Number); closeErr != nil {
								slog.Error("Failed to close stuck PR", "pr", pr.Number, "error", closeErr)
							} else {
								log.Printf("PR #%d closed - associated issues will spawn new PRs on next run", pr.Number)
							}
						} else {
							slog.Error("Failed to resolve authority conflicts", "pr", pr.Number, "error", resolveErr)
							log.Printf("PR #%d needs manual conflict resolution", pr.Number)
						}
					} else {
						// Successfully resolved conflicts by pushing new commits to the PR branch.
						// Don't try to merge immediately - CI needs to run on the new commits first,
						// and GitHub needs time to recalculate the mergeable status.
						log.Printf("PR #%d conflicts resolved by merging authority files - CI will re-run, will check on next run", pr.Number)
					}
				}
			} else {
				// GitHub's UpdateBranch succeeded - the base branch was merged into the PR branch.
				// CI needs to run on the new head before we can merge.
				log.Printf("PR #%d branch updated from main - CI will re-run, will check on next run", pr.Number)
			}
		}
	}

	if mergedCount > 0 {
		log.Printf("Merged %d PRs this run", mergedCount)
	}

	return nil
}

func (a *Agent) processTopic(ctx context.Context, topic, category, branchName string, authMgr *authority.Manager, progress *ProgressTracker) error {
	slug := strings.ToLower(strings.ReplaceAll(topic, " ", "-"))

	// Research - generate multiple search queries for variety
	numQueries := 5
	if envVal := os.Getenv("PHASE1_SEARCH_NUM_QUERIES"); envVal != "" {
		if v, err := strconv.Atoi(envVal); err == nil && v > 0 {
			numQueries = v
		}
	}
	// Backwards compatibility with old variable name
	if numQueries == 5 {
		if envVal := os.Getenv("SEARCH_NUM_QUERIES"); envVal != "" {
			if v, err := strconv.Atoi(envVal); err == nil && v > 0 {
				numQueries = v
			}
		}
	}

	baseQueries := []string{
		topic + " encyclopedia facts",
		topic + " history context",
		topic + " summary overview",
		topic + " definition explanation",
		topic + " applications uses",
		topic + " current research",
		topic + " key concepts",
		topic + " notable examples",
	}

	queries := baseQueries
	if numQueries < len(baseQueries) {
		queries = baseQueries[:numQueries]
	} else if numQueries > len(baseQueries) {
		// Extend with variations if more queries requested
		extra := numQueries - len(baseQueries)
		for i := 0; i < extra; i++ {
			queries = append(queries, fmt.Sprintf("%s information", topic))
		}
	}

	type resultWithQuery struct {
		result     search.Result
		queryIndex int
	}

	var results []resultWithQuery
	seenURLs := make(map[string]bool)

	for queryIdx, q := range queries {
		// Check for cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		res, err := a.search.Search(q)
		if err != nil {
			log.Printf("Search warning for '%s': %v", q, err)
			continue
		}
		log.Printf("Search '%s' returned %d results", q, len(res))
		for _, r := range res {
			if !seenURLs[r.Href] {
				results = append(results, resultWithQuery{result: r, queryIndex: queryIdx})
				seenURLs[r.Href] = true
			}
		}

		// Check for cancellation before sleeping
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}

	// Deep Research: Fetch content for top results
	contextData := "Sources:\n"
	var references []string

	limit := 30
	if envVal := os.Getenv("PHASE1_TARGET_SOURCES"); envVal != "" {
		if v, err := strconv.Atoi(envVal); err == nil && v > 0 {
			limit = v
		}
	}
	// Backwards compatibility with old variable names
	if limit == 30 {
		if envVal := os.Getenv("TARGET_SOURCES_PHASE1"); envVal != "" {
			if v, err := strconv.Atoi(envVal); err == nil && v > 0 {
				limit = v
			}
		}
	}
	if limit == 30 {
		if envVal := os.Getenv("SEARCH_MAX_SOURCES"); envVal != "" {
			if v, err := strconv.Atoi(envVal); err == nil && v > 0 {
				limit = v
			}
		}
	}
	log.Printf("Total unique search results: %d (target successful summaries: %d)", len(results), limit)

	// Prepare targets - include ALL non-PDF results so we can keep processing
	// until we reach the target number of successful summaries
	type targetWithQuery struct {
		result     search.Result
		queryIndex int
		index      int // Original index in targets slice
	}

	var targets []targetWithQuery
	targetIndex := 0
	for _, rwq := range results {
		if !strings.HasSuffix(rwq.result.Href, ".pdf") {
			targets = append(targets, targetWithQuery{
				result:     rwq.result,
				queryIndex: rwq.queryIndex,
				index:      targetIndex,
			})
			targetIndex++
		}
	}
	log.Printf("Prepared %d targets for fetching (excluding PDFs)", len(targets))

	// Set the target for progress tracking (PHASE1_TARGET_SOURCES)
	if progress != nil {
		progress.SetTotal(limit)
	}

	// Adjust limit if we don't have enough targets
	if len(targets) < limit {
		slog.Warn("Fewer targets available than limit", "available", len(targets), "limit", limit)
		limit = len(targets)
	}

	// Fetch content in parallel
	type fetchResult struct {
		index   int
		content string
		err     error
	}
	resultsChan := make(chan fetchResult, len(targets))
	var wg sync.WaitGroup

	// Use a semaphore to limit concurrency if needed (configurable, default 3)
	semSize := 3
	if envVal := os.Getenv("SEARCH_MAX_FETCH_CONCURRENCY"); envVal != "" {
		if v, err := strconv.Atoi(envVal); err == nil && v > 0 {
			semSize = v
		}
	}
	sem := make(chan struct{}, semSize)

	for _, t := range targets {
		wg.Add(1)
		go func(idx int, urlStr string) {
			defer wg.Done()
			sem <- struct{}{}        // Acquire
			defer func() { <-sem }() // Release

			content, err := a.search.FetchContent(urlStr)
			resultsChan <- fetchResult{idx, content, err}
		}(t.index, t.result.Href)
	}

	// Collect fetched page contents
	fetchedContents := make(map[int]string)

	// Start a goroutine to collect results
	done := make(chan bool)
	go func() {
		for res := range resultsChan {
			if res.err == nil && len(res.content) >= 100 {
				fetchedContents[res.index] = res.content
			} else if res.err != nil {
				slog.Warn("Failed to fetch URL", "url", targets[res.index].result.Href, "error", res.err)
			}
		}
		done <- true
	}()

	wg.Wait()
	close(resultsChan)
	<-done // Wait for collection to finish

	log.Printf("Successfully fetched %d/%d sources", len(fetchedContents), len(targets))

	// Target number of summaries to collect per query
	targetSummariesPerQuery := 2
	if envVal := os.Getenv("TARGET_SUMMARIES_PER_QUERY"); envVal != "" {
		if v, err := strconv.Atoi(envVal); err == nil && v > 0 {
			targetSummariesPerQuery = v
		}
	}

	// Track summaries collected per query
	summariesPerQuery := make(map[int]int)
	processedCount := 0
	skippedCount := 0
	skippedReasons := make(map[string]int)

	// Optional debug mode: when enabled, save raw fetched pages and LLM outputs
	debugSources := false
	if v := os.Getenv("RESEARCH_DEBUG_SOURCES"); strings.EqualFold(v, "1") || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes") {
		debugSources = true
	}
	for _, t := range targets {
		// Check for cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		content, ok := fetchedContents[t.index]
		if !ok {
			skippedCount++
			skippedReasons["fetch failed"]++
			continue
		}

		// If debug is enabled, save the raw fetched page content for inspection.
		if debugSources {
			if u, err := url.Parse(t.result.Href); err == nil {
				domain := strings.ReplaceAll(u.Host, ".", "-")
				rawPath := fmt.Sprintf("Compendium/_debug/sources/%s--%s-%d/raw.txt", slug, domain, t.index+1)
				if err := a.gh.CreateFile(branchName, rawPath, "Add debug raw source: "+t.result.Title, content); err != nil {
					slog.Warn("Failed to save debug raw source", "path", rawPath, "error", err)
				}
			}
		}

		// Summarize and filter source using LLM
		log.Printf("Summarizing source %d/%d: %s", processedCount+1, limit, t.result.Href)
		summary, err := a.llm.SummarizeSource(ctx, topic, t.result.Href, content)
		if err != nil {
			slog.Warn("Failed to summarize source", "url", t.result.Href, "error", err)
			// If we have the raw LLM output, save it for debugging.
			if debugSources {
				if u, errParse := url.Parse(t.result.Href); errParse == nil {
					domain := strings.ReplaceAll(u.Host, ".", "-")
					// Save step 1 output if available
					if summary.Step1Output != "" {
						step1Path := fmt.Sprintf("Compendium/_debug/sources/%s--%s-%d/phase_1/step_1.txt", slug, domain, t.index+1)
						if errSave := a.gh.CreateFile(branchName, step1Path, "Add debug phase 1 step 1 output (error): "+t.result.Title, summary.Step1Output); errSave != nil {
							slog.Warn("Failed to save debug step 1 output", "path", step1Path, "error", errSave)
						}
					}
					// Save step 2 output if available
					if summary.Raw != "" {
						step2Path := fmt.Sprintf("Compendium/_debug/sources/%s--%s-%d/phase_1/step_2.txt", slug, domain, t.index+1)
						if errSave := a.gh.CreateFile(branchName, step2Path, "Add debug phase 1 step 2 output (error): "+t.result.Title, summary.Raw); errSave != nil {
							slog.Warn("Failed to save debug step 2 output", "path", step2Path, "error", errSave)
						}
					}
				}
			}
			continue
		}

		// In debug mode, save both step outputs for the source.
		if debugSources {
			if u, err := url.Parse(t.result.Href); err == nil {
				domain := strings.ReplaceAll(u.Host, ".", "-")
				// Save phase 1 step 1 output (plain-text summarization)
				if summary.Step1Output != "" {
					step1Path := fmt.Sprintf("Compendium/_debug/sources/%s--%s-%d/phase_1/step_1.txt", slug, domain, t.index+1)
					if err := a.gh.CreateFile(branchName, step1Path, "Add debug phase 1 step 1 output: "+t.result.Title, summary.Step1Output); err != nil {
						slog.Warn("Failed to save debug step 1 output", "path", step1Path, "error", err)
					}
				}
				// Save phase 1 step 2 output (JSON conversion)
				if summary.Raw != "" {
					step2Path := fmt.Sprintf("Compendium/_debug/sources/%s--%s-%d/phase_1/step_2.txt", slug, domain, t.index+1)
					if err := a.gh.CreateFile(branchName, step2Path, "Add debug phase 1 step 2 output: "+t.result.Title, summary.Raw); err != nil {
						slog.Warn("Failed to save debug step 2 output", "path", step2Path, "error", err)
					}
				}
			}
		}

		// In debug mode, save step outputs even if source is not relevant
		if !summary.Relevant {
			if debugSources {
				if u, err := url.Parse(t.result.Href); err == nil {
					domain := strings.ReplaceAll(u.Host, ".", "-")
					// Save phase 1 step 1 output (plain-text summarization)
					if summary.Step1Output != "" {
						step1Path := fmt.Sprintf("Compendium/_debug/sources/%s--%s-%d/phase_1/step_1.txt", slug, domain, t.index+1)
						if err := a.gh.CreateFile(branchName, step1Path, "Add debug phase 1 step 1 output (not relevant): "+t.result.Title, summary.Step1Output); err != nil {
							slog.Warn("Failed to save debug step 1 output", "path", step1Path, "error", err)
						}
					}
					// Save phase 1 step 2 output if available (may not exist if marked NOT_RELEVANT in step 1)
					if summary.Raw != "" && summary.Raw != summary.Step1Output {
						step2Path := fmt.Sprintf("Compendium/_debug/sources/%s--%s-%d/phase_1/step_2.txt", slug, domain, t.index+1)
						if err := a.gh.CreateFile(branchName, step2Path, "Add debug phase 1 step 2 output (not relevant): "+t.result.Title, summary.Raw); err != nil {
							slog.Warn("Failed to save debug step 2 output", "path", step2Path, "error", err)
						}
					}
				}
			}
			log.Printf("Skipping source as not relevant: %s (reason: %s)", t.result.Href, summary.Reason)
			skippedCount++
			skippedReasons["not relevant"]++
			continue
		}

		// Validate summary length (configurable via environment variables)
		minWords := 400
		if envVal := os.Getenv("SOURCE_SUMMARY_MIN_WORDS"); envVal != "" {
			if v, err := strconv.Atoi(envVal); err == nil && v > 0 {
				minWords = v
			}
		}

		wordCount := len(strings.Fields(summary.Summary))
		if wordCount < minWords {
			log.Printf("Skipping source %s: summary too short (%d words, minimum %d)", t.result.Href, wordCount, minWords)
			skippedCount++
			skippedReasons["too short"]++
			continue
		}
		// Summaries above minimum are accepted (target is 1200-2000 but not enforced)

		// Check if this query has already reached its target (only count successful summaries)
		if summariesPerQuery[t.queryIndex] >= targetSummariesPerQuery {
			log.Printf("Skipping source from query %d (limit %d successful summaries reached): %s - continuing to other queries", t.queryIndex, targetSummariesPerQuery, t.result.Href)
			skippedCount++
			skippedReasons["query limit reached"]++
			continue
		}

		// Increment summary count for this query (only successful summaries count)
		summariesPerQuery[t.queryIndex]++
		processedCount++

		// Print progress after each successful summary
		if progress != nil {
			progress.Update(processedCount)
		}
		contextData += fmt.Sprintf("[%d] Title: %s\nURL: %s\nSummary: %s\n\n", processedCount, t.result.Title, t.result.Href, summary.Summary)
		references = append(references, fmt.Sprintf("[^%d]: [%s](%s)", processedCount, t.result.Title, t.result.Href))

		// Save source summary with extracted entities
		if u, err := url.Parse(t.result.Href); err == nil {
			domain := strings.ReplaceAll(u.Host, ".", "-")
			sourceID := ulid.Make().String()
			sourcePath := fmt.Sprintf("Compendium/_incoming/sources/%s--%s-%d.md", slug, domain, processedCount)

			// Extract entities from source content for knowledge base ingestion
			sourceEntities, err := a.llm.ExtractEntities(ctx, summary.Summary)
			if err != nil {
				slog.Warn("Entity extraction failed for source", "error", err)
				sourceEntities = []llm.ExtractedEntity{}
			}

			// Add the parent topic as an entity
			sourceEntities = append(sourceEntities, llm.ExtractedEntity{Name: topic, Type: llm.Topic})

			// Resolve entities to authority IDs
			resolvedSource, err := authMgr.ResolveEntities(sourceEntities)
			if err != nil {
				slog.Warn("Entity resolution failed for source", "error", err)
				resolvedSource = make(map[string][]string)
			}

			// Build tags from topics
			var sourceTags []string
			if topicIDs, ok := resolvedSource["topic"]; ok {
				sourceTags = topicIDs
			}
			sourceTagsStr := fmt.Sprintf("[\"%s\"]", strings.Join(sourceTags, "\", \""))

			// Build facets block for people, orgs, places
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

			// Build front matter with optional fields
			modelField := ""
			if summary.Model != "" {
				modelField = fmt.Sprintf("model: \"%s\"\n", summary.Model)
			}
			languageField := ""
			if summary.Language != "" {
				languageField = fmt.Sprintf("language: \"%s\"\n", summary.Language)
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
%s%s---

%s
`, sourceID, slug, domain, processedCount, t.result.Title, t.result.Href, slug, time.Now().Format("2006-01-02"), sourceTagsStr, sourceFacets, topic, modelField, languageField, summary.Summary)

			if err := a.gh.CreateFile(branchName, sourcePath, "Add source: "+t.result.Title, sourceContent); err != nil {
				slog.Error("Failed to save source", "path", sourcePath, "error", err)
				// Check if this is a 401 error - if so, stop processing as token is invalid
				if strings.Contains(err.Error(), "401") {
					return fmt.Errorf("authentication failed while saving source: %w", err)
				}
			}
		}

		// Stop Phase 1 if we've reached the target number of sources (PHASE1_TARGET_SOURCES)
		if processedCount >= limit {
			log.Printf("Reached Phase 1 target of %d summaries, stopping source processing", limit)
			break
		}
	}

	// Log summary of processing results
	if processedCount < limit {
		log.Printf("Phase 1 completed with %d/%d sources (target: %d). Skipped %d sources:", processedCount, len(targets), limit, skippedCount)
		// Show per-query summary
		log.Printf("Summaries per query:")
		for qIdx, count := range summariesPerQuery {
			log.Printf("  - Query %d: %d summaries (limit: %d)", qIdx, count, targetSummariesPerQuery)
		}
		for reason, count := range skippedReasons {
			log.Printf("  - %s: %d", reason, count)
		}
		if processedCount == 0 {
			return fmt.Errorf("no valid sources collected for topic %s", topic)
		}
	} else {
		log.Printf("Phase 1 completed: collected %d/%d sources", processedCount, limit)
		// Show per-query summary
		log.Printf("Summaries per query:")
		for qIdx, count := range summariesPerQuery {
			log.Printf("  - Query %d: %d summaries (limit: %d)", qIdx, count, targetSummariesPerQuery)
		}
	}

	// Draft
	articleResult, err := a.llm.GenerateArticle(ctx, topic, contextData)
	if err != nil {
		return fmt.Errorf("generation failed: %w", err)
	}
	content := articleResult.Content
	articleModel := articleResult.Model

	// Strip code fences if the LLM wrapped the content in them
	// Some LLMs wrap YAML frontmatter in ```yaml ... ``` which breaks rendering
	content = stripCodeFences(content)

	// Append References
	if len(references) > 0 {
		content += "\n\n## References\n\n" + strings.Join(references, "\n")
	}

	// Entities
	extracted, err := a.llm.ExtractEntities(ctx, content)
	if err != nil {
		slog.Warn("Entity extraction failed", "error", err)
	}

	// Add Category as a topic if missing
	foundCat := false
	for _, e := range extracted {
		if strings.EqualFold(e.Name, category) {
			foundCat = true
			break
		}
	}
	if !foundCat {
		extracted = append(extracted, llm.ExtractedEntity{Name: category, Type: llm.Topic})
	}
	// Add Topic itself
	extracted = append(extracted, llm.ExtractedEntity{Name: topic, Type: llm.Topic})

	resolved, err := authMgr.ResolveEntities(extracted)
	if err != nil {
		slog.Warn("Entity resolution failed", "error", err)
	}

	// Front Matter
	id := ulid.Make()
	// slug is already defined at start of function
	date := time.Now().Format("2006-01-02")

	var tags []string
	if topicIDs, ok := resolved["topic"]; ok {
		tags = topicIDs
	}
	tagsStr := fmt.Sprintf("[\"%s\"]", strings.Join(tags, "\", \""))

	facetsBlock := ""
	if ids, ok := resolved["person"]; ok && len(ids) > 0 {
		facetsBlock += fmt.Sprintf("people: [\"%s\"]\n", strings.Join(ids, "\", \""))
	}
	if ids, ok := resolved["org"]; ok && len(ids) > 0 {
		facetsBlock += fmt.Sprintf("orgs: [\"%s\"]\n", strings.Join(ids, "\", \""))
	}
	if ids, ok := resolved["place"]; ok && len(ids) > 0 {
		facetsBlock += fmt.Sprintf("places: [\"%s\"]\n", strings.Join(ids, "\", \""))
	}

	// Build model field for frontmatter
	modelField := ""
	if articleModel != "" {
		modelField = fmt.Sprintf("model: \"%s\"\n", articleModel)
	}

	var fullContent string
	if strings.HasPrefix(strings.TrimSpace(content), "---") {
		lines := strings.Split(content, "\n")
		if len(lines) > 1 {
			systemFields := fmt.Sprintf("id: %s\nslug: \"%s\"\ncreated: %s", id, slug, date)
			var cleanedLines []string
			for _, line := range lines {
				if strings.HasPrefix(strings.TrimSpace(line), "tags:") {
					continue
				}
				cleanedLines = append(cleanedLines, line)
			}
			injection := fmt.Sprintf("%s\ntags: %s\n%s%s", systemFields, tagsStr, facetsBlock, modelField)
			newLines := append([]string{cleanedLines[0], injection}, cleanedLines[1:]...)
			fullContent = strings.Join(newLines, "\n")
		} else {
			fullContent = content
		}
	} else {
		frontMatter := fmt.Sprintf(`---
id: %s
title: "%s"
slug: "%s"
created: %s
tags: %s
%s%ssummary: ""
---

`, id, topic, slug, date, tagsStr, facetsBlock, modelField)
		fullContent = frontMatter + content
	}

	filePath := fmt.Sprintf("Compendium/_incoming/%s.md", slug)
	return a.gh.CreateFile(branchName, filePath, fmt.Sprintf("Add article: %s", topic), fullContent)
}

// stripCodeFences removes markdown code fences that wrap YAML frontmatter
// Some LLMs incorrectly wrap the entire article or frontmatter in ```yaml ... ``` blocks
func stripCodeFences(content string) string {
	content = strings.TrimSpace(content)

	// Check if content starts with a code fence
	if !strings.HasPrefix(content, "```") {
		return content
	}

	// Find the first newline after the opening fence
	firstNewline := strings.Index(content, "\n")
	if firstNewline == -1 {
		return content
	}

	// Remove the opening fence line (e.g., "```yaml" or "```markdown")
	content = content[firstNewline+1:]

	// Find and remove the closing fence
	lastFence := strings.LastIndex(content, "```")
	if lastFence != -1 {
		content = content[:lastFence]
	}

	return strings.TrimSpace(content)
}
