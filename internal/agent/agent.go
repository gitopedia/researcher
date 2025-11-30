package agent

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"net/url"
	"os"
	"os/exec"
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
		log.Printf("Warning: error checking/merging PRs: %v", err)
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

	// Get open PRs to filter out issues that already have PRs
	openPRs, err := a.gh.ListOpenPRs()
	if err != nil {
		log.Printf("Warning: failed to list open PRs: %v", err)
		openPRs = nil
	}

	// Build set of issue numbers that have open PRs
	issuesWithPRs := make(map[int]bool)
	for _, pr := range openPRs {
		for _, issueNum := range pr.IssueRefs {
			issuesWithPRs[issueNum] = true
			log.Printf("Issue #%d has open PR #%d, will skip", issueNum, pr.Number)
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
		log.Println("All research issues already have open PRs. Nothing to do.")
		return nil
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
		log.Printf("Warning: error checking/merging PRs after research: %v", err)
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
		log.Printf("Warning: failed to load authorities: %v", err)
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
			log.Printf("Error processing topic '%s': %v", topic, err)
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
		log.Printf("Failed to get authority updates: %v", err)
	} else {
		for path, update := range updates {
			log.Printf("Updating authority file: %s", path)
			if update.SHA == "" {
				if err := a.gh.CreateFile(branchName, path, "Create authority "+path, update.Content); err != nil {
					log.Printf("Failed to create authority file %s: %v", path, err)
				}
			} else {
				if err := a.gh.UpdateFile(branchName, path, "Update authority "+path, update.Content, update.SHA); err != nil {
					log.Printf("Failed to update authority file %s: %v", path, err)
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
		log.Printf("Failed to comment on issue: %v", err)
	}

	// 9. Invoke Encyclopaedist agent (non-blocking)
	if err := a.invokeEncyclopaedist(ctx, *pr.Number); err != nil {
		log.Printf("Warning: failed to invoke Encyclopaedist: %v", err)
	}

	log.Printf("Research task complete. PR #%d created and Encyclopaedist invoked.", *pr.Number)
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
	for _, pr := range openPRs {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		status, err := a.gh.GetPRStatus(pr.Number)
		if err != nil {
			log.Printf("Warning: failed to get status for PR #%d: %v", pr.Number, err)
			continue
		}

		log.Printf("PR #%d: draft=%v, state=%s, ci=%s, mergeable=%v",
			pr.Number, status.Draft, status.State, status.CIStatus, status.Mergeable)

		// Check if ready to merge: not draft, CI passed, mergeable
		if !status.Draft && status.CIStatus == "success" && status.Mergeable {
			log.Printf("PR #%d is ready to merge!", pr.Number)

			commitMsg := fmt.Sprintf("Merge PR #%d: automated content expansion", pr.Number)
			if err := a.gh.MergePR(pr.Number, commitMsg); err != nil {
				log.Printf("Failed to merge PR #%d: %v", pr.Number, err)
				continue
			}
			log.Printf("Successfully merged PR #%d", pr.Number)
			mergedCount++

			// Close the tracking issues
			for _, issueNum := range pr.IssueRefs {
				if err := a.gh.CloseIssue(issueNum); err != nil {
					log.Printf("Warning: failed to close issue #%d: %v", issueNum, err)
				} else {
					log.Printf("Closed tracking issue #%d", issueNum)
				}
			}
		} else if status.CIStatus == "failure" {
			log.Printf("PR #%d has failed CI - needs manual attention", pr.Number)
		} else if status.Draft {
			log.Printf("PR #%d is still a draft - waiting for Encyclopaedist", pr.Number)
		} else if status.CIStatus == "pending" {
			log.Printf("PR #%d CI is still running", pr.Number)
		}
	}

	if mergedCount > 0 {
		log.Printf("Merged %d PRs this run", mergedCount)
	}

	return nil
}

// invokeEncyclopaedist uses gh CLI to trigger the Encyclopaedist Copilot agent
func (a *Agent) invokeEncyclopaedist(ctx context.Context, prNumber int) error {
	log.Printf("Invoking Encyclopaedist agent for PR #%d via gh copilot CLI", prNumber)

	// Build the prompt for Copilot
	prompt := fmt.Sprintf(`You are the Encyclopaedist agent. Process PR #%d in the gitopedia/gitopedia repository.

Your tasks:
1. List all .md files in Compendium/_incoming/ (excluding sources/ subdirectory)
2. For each article, analyze tags and content to determine the appropriate Compendium/<Category>/ path
3. Move each article from _incoming/<slug>.md to Compendium/<Category>/<slug>.md
4. Validate front matter has: id (ULID), title, slug, created, tags
5. Delete any _debug/ directories if present
6. Leave _incoming/sources/ untouched
7. Commit changes and mark the PR ready for review

Use gh CLI to checkout the PR branch and make the changes.`, prNumber)

	// Use exec to run gh copilot
	cmd := exec.CommandContext(ctx, "gh", "copilot", "suggest", "-t", "shell", prompt)
	cmd.Dir = os.Getenv("GITOPEDIA_REPO_PATH")
	if cmd.Dir == "" {
		cmd.Dir = "."
	}
	
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Log the output for debugging
		log.Printf("gh copilot output: %s", string(output))
		
		// Fallback: just add a comment with instructions
		log.Printf("gh copilot failed, adding manual instruction comment")
		comment := fmt.Sprintf(`🤖 **Encyclopaedist Instructions**

This PR needs organization. Please run the Encyclopaedist agent manually:

%s

Or use Copilot Chat with the Encyclopaedist agent to process this PR.`, "```\ngh copilot suggest -t shell \"Process PR #"+fmt.Sprint(prNumber)+" as Encyclopaedist\"\n```")
		
		if commentErr := a.gh.CommentOnPR(prNumber, comment); commentErr != nil {
			log.Printf("Failed to add fallback comment: %v", commentErr)
		}
		
		return fmt.Errorf("gh copilot failed: %w (output: %s)", err, string(output))
	}

	log.Printf("Encyclopaedist invoked successfully for PR #%d", prNumber)
	log.Printf("gh copilot output: %s", string(output))
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
		log.Printf("Warning: only %d targets available, reducing limit from %d", len(targets), limit)
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
				log.Printf("Failed to fetch %s: %v", targets[res.index].result.Href, res.err)
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
					log.Printf("Failed to save debug raw source %s: %v", rawPath, err)
				}
			}
		}

		// Summarize and filter source using LLM
		log.Printf("Summarizing source %d/%d: %s", processedCount+1, limit, t.result.Href)
		summary, err := a.llm.SummarizeSource(ctx, topic, t.result.Href, content)
		if err != nil {
			log.Printf("Failed to summarize %s: %v", t.result.Href, err)
			// If we have the raw LLM output, save it for debugging.
			if debugSources {
				if u, errParse := url.Parse(t.result.Href); errParse == nil {
					domain := strings.ReplaceAll(u.Host, ".", "-")
					// Save step 1 output if available
					if summary.Step1Output != "" {
						step1Path := fmt.Sprintf("Compendium/_debug/sources/%s--%s-%d/phase_1/step_1.txt", slug, domain, t.index+1)
						if errSave := a.gh.CreateFile(branchName, step1Path, "Add debug phase 1 step 1 output (error): "+t.result.Title, summary.Step1Output); errSave != nil {
							log.Printf("Failed to save debug step 1 output %s: %v", step1Path, errSave)
						}
					}
					// Save step 2 output if available
					if summary.Raw != "" {
						step2Path := fmt.Sprintf("Compendium/_debug/sources/%s--%s-%d/phase_1/step_2.txt", slug, domain, t.index+1)
						if errSave := a.gh.CreateFile(branchName, step2Path, "Add debug phase 1 step 2 output (error): "+t.result.Title, summary.Raw); errSave != nil {
							log.Printf("Failed to save debug step 2 output %s: %v", step2Path, errSave)
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
						log.Printf("Failed to save debug step 1 output %s: %v", step1Path, err)
					}
				}
				// Save phase 1 step 2 output (JSON conversion)
				if summary.Raw != "" {
					step2Path := fmt.Sprintf("Compendium/_debug/sources/%s--%s-%d/phase_1/step_2.txt", slug, domain, t.index+1)
					if err := a.gh.CreateFile(branchName, step2Path, "Add debug phase 1 step 2 output: "+t.result.Title, summary.Raw); err != nil {
						log.Printf("Failed to save debug step 2 output %s: %v", step2Path, err)
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
							log.Printf("Failed to save debug step 1 output %s: %v", step1Path, err)
						}
					}
					// Save phase 1 step 2 output if available (may not exist if marked NOT_RELEVANT in step 1)
					if summary.Raw != "" && summary.Raw != summary.Step1Output {
						step2Path := fmt.Sprintf("Compendium/_debug/sources/%s--%s-%d/phase_1/step_2.txt", slug, domain, t.index+1)
						if err := a.gh.CreateFile(branchName, step2Path, "Add debug phase 1 step 2 output (not relevant): "+t.result.Title, summary.Raw); err != nil {
							log.Printf("Failed to save debug step 2 output %s: %v", step2Path, err)
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
				log.Printf("Warning: entity extraction failed for source: %v", err)
				sourceEntities = []llm.ExtractedEntity{}
			}

			// Add the parent topic as an entity
			sourceEntities = append(sourceEntities, llm.ExtractedEntity{Name: topic, Type: llm.Topic})

			// Resolve entities to authority IDs
			resolvedSource, err := authMgr.ResolveEntities(sourceEntities)
			if err != nil {
				log.Printf("Warning: entity resolution failed for source: %v", err)
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
				log.Printf("Failed to save source %s: %v", sourcePath, err)
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
	content, err := a.llm.GenerateArticle(ctx, topic, contextData)
	if err != nil {
		return fmt.Errorf("generation failed: %w", err)
	}

	// Append References
	if len(references) > 0 {
		content += "\n\n## References\n\n" + strings.Join(references, "\n")
	}

	// Entities
	extracted, err := a.llm.ExtractEntities(ctx, content)
	if err != nil {
		log.Printf("Warning: entity extraction failed: %v", err)
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
		log.Printf("Warning: entity resolution failed: %v", err)
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
			injection := fmt.Sprintf("%s\ntags: %s\n%s", systemFields, tagsStr, facetsBlock)
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
%ssummary: ""
---

`, id, topic, slug, date, tagsStr, facetsBlock)
		fullContent = frontMatter + content
	}

	filePath := fmt.Sprintf("Compendium/_incoming/%s.md", slug)
	return a.gh.CreateFile(branchName, filePath, fmt.Sprintf("Add article: %s", topic), fullContent)
}
