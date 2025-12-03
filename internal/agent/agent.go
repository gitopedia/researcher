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

// Version is set at build time or read from VERSION file
var Version = "dev"

func init() {
	// Try to read version from VERSION file if not set at build time
	if Version == "dev" {
		if data, err := os.ReadFile("VERSION"); err == nil {
			Version = strings.TrimSpace(string(data))
		}
	}
}

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

// MergeOnly runs only the PR merging logic without processing new issues
func (a *Agent) MergeOnly(ctx context.Context) error {
	log.Println("Running merge-only mode...")
	return a.mergeReadyPRs(ctx)
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

	// 1. Context: List existing articles (excluding sources, debug, and incoming)
	log.Println("Listing existing articles...")
	files, err := a.gh.ListAllFiles("Compendium")
	if err != nil {
		return fmt.Errorf("failed to list files: %w", err)
	}
	var existingTitles []string
	for _, f := range files {
		// Skip non-markdown files
		if !strings.HasSuffix(f, ".md") {
			continue
		}
		// Skip index files
		if strings.HasSuffix(f, "index.md") {
			continue
		}
		// Skip _incoming directory (pending articles and sources)
		if strings.Contains(f, "_incoming") {
			continue
		}
		// Skip _debug directory
		if strings.Contains(f, "_debug") {
			continue
		}
		// Convert filename to title roughly (slug to title logic is fuzzy but good enough for LLM context)
		// e.g. Compendium/Technology/AI/OpenAI.md -> OpenAI
		base := filepath.Base(f)
		name := strings.TrimSuffix(base, filepath.Ext(base))
		existingTitles = append(existingTitles, name)
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
			log.Printf("PR #%d has failed CI - investigating...", pr.Number)
			// Try to get CI logs to understand the failure
			if logs, err := a.gh.GetFailedCILogs(pr.Number); err == nil && logs != "" {
				log.Printf("PR #%d CI failure details:\n%s", pr.Number, logs)
				
				// Check for common fixable issues
				if strings.Contains(logs, "yaml:") || strings.Contains(logs, "front matter error") {
					log.Printf("PR #%d: YAML parsing error detected - this may be due to invalid characters in entity names", pr.Number)
					log.Printf("PR #%d: Consider checking the article frontmatter for quotes or special characters", pr.Number)
				}
			} else if err != nil {
				slog.Warn("Could not fetch CI logs", "pr", pr.Number, "error", err)
			}
			log.Printf("PR #%d needs manual attention to fix CI failure", pr.Number)
		} else if status.Draft {
			log.Printf("PR #%d is still a draft - waiting for Encyclopaedist", pr.Number)
		} else if status.CIStatus == "pending" {
			log.Printf("PR #%d CI is still running", pr.Number)
		} else if status.Mergeable == nil {
			// Mergeable status is unknown - GitHub is still calculating (often after new commits)
			log.Printf("PR #%d mergeable status unknown - GitHub is calculating, will check on next run", pr.Number)
		} else if !*status.Mergeable {
			// PR has conflicts - try to update the branch by merging main into it
			log.Printf("PR #%d has merge conflicts (mergeable=false) - attempting to resolve...", pr.Number)
			log.Printf("PR #%d: Head branch = %s", pr.Number, pr.HeadBranch)
			if pr.HeadBranch == "" {
				slog.Error("Cannot resolve conflicts: PR has no head branch info", "pr", pr.Number)
				continue
			}

			// Try GitHub's simple UpdateBranch first (works for non-conflicting cases)
			log.Printf("PR #%d: Trying GitHub's UpdateBranch API first...", pr.Number)
			if err := a.gh.UpdatePRBranch(pr.Number); err != nil {
				// GitHub's UpdateBranch failed - need to create a merge commit manually
				log.Printf("PR #%d: GitHub UpdateBranch failed: %v", pr.Number, err)
				log.Printf("PR #%d: Creating merge commit with conflict resolution...", pr.Number)
				if resolveErr := a.gh.CreateMergeCommitWithResolution(pr.HeadBranch); resolveErr != nil {
					slog.Error("Failed to create merge commit", "pr", pr.Number, "branch", pr.HeadBranch, "error", resolveErr)
					log.Printf("PR #%d needs manual conflict resolution - error: %v", pr.Number, resolveErr)
				} else {
					log.Printf("PR #%d: merge commit created successfully - CI will re-run, will check on next run", pr.Number)
				}
			} else {
				log.Printf("PR #%d: GitHub UpdateBranch succeeded - CI will re-run, will check on next run", pr.Number)
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
	config := GetPhaseConfig()

	// Optional debug mode
	debugSources := false
	if v := os.Getenv("RESEARCH_DEBUG_SOURCES"); strings.EqualFold(v, "1") || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes") {
		debugSources = true
	}

	// ========================================
	// PHASE 1: Foundation Research
	// ========================================
	log.Printf("=== PHASE 1: Foundation Research for '%s' ===", topic)

	sources, references, err := a.gatherSources(ctx, topic, slug, branchName, authMgr, progress, debugSources)
	if err != nil {
		return fmt.Errorf("phase 1 failed: %w", err)
	}

	if len(sources) == 0 {
		return fmt.Errorf("no valid sources collected for topic %s", topic)
	}

	// Generate outline from sources
	outline, err := a.Phase1GenerateOutline(ctx, topic, sources)
	if err != nil {
		slog.Warn("Outline generation failed, falling back to simple structure", "error", err)
		// Create a simple default outline
		outline = &ArticleOutline{
			Title:           topic,
			Summary:         fmt.Sprintf("An overview of %s", topic),
			TotalWordTarget: 3000,
			Sections: []SectionOutline{
				{Heading: "Overview", Level: 2, WordTarget: 500, Points: []string{"Introduction", "Key concepts"}},
				{Heading: "History", Level: 2, WordTarget: 600, Points: []string{"Origins", "Development"}},
				{Heading: "Key Aspects", Level: 2, WordTarget: 800, Points: []string{"Main features", "Characteristics"}},
				{Heading: "Applications", Level: 2, WordTarget: 600, Points: []string{"Uses", "Impact"}},
				{Heading: "Current State", Level: 2, WordTarget: 500, Points: []string{"Modern developments", "Future"}},
			},
		}
	}

	// ========================================
	// PHASE 2: Gap Analysis
	// ========================================
	log.Printf("=== PHASE 2: Gap Analysis ===")

	gaps, err := a.Phase2AnalyzeGaps(ctx, topic, outline, sources)
	if err != nil {
		slog.Warn("Gap analysis failed, proceeding without gap filling", "error", err)
		gaps = &GapAnalysis{Gaps: nil, SuggestedSections: nil}
	}

	// ========================================
	// PHASE 3: Targeted Research (if gaps found)
	// ========================================
	allSources := sources
	maxRounds := config.MaxResearchRounds
	for round := 0; round < maxRounds && len(gaps.Gaps) > 0; round++ {
		log.Printf("=== PHASE 3: Targeted Research (Round %d/%d) ===", round+1, maxRounds)

		newSources, err := a.Phase3TargetedResearch(ctx, gaps, allSources)
		if err != nil {
			slog.Warn("Targeted research failed", "round", round+1, "error", err)
			break
		}

		if len(newSources) == 0 {
			log.Printf("No new sources found in round %d", round+1)
			break
		}

		// Save new sources to _incoming/sources
		for _, src := range newSources {
			if err := a.saveSourceSummary(ctx, src, topic, slug, branchName, authMgr, debugSources); err != nil {
				slog.Warn("Failed to save new source", "url", src.URL, "error", err)
			}
			references = append(references, fmt.Sprintf("[^%d]: [%s](%s)", src.Index, src.Title, src.URL))
		}

		allSources = append(allSources, newSources...)

		// Re-analyze gaps with new sources
		gaps, err = a.Phase2AnalyzeGaps(ctx, topic, outline, allSources)
		if err != nil {
			slog.Warn("Re-analysis failed", "error", err)
			break
		}
	}

	// ========================================
	// PHASE 4: Section-by-Section Generation
	// ========================================
	log.Printf("=== PHASE 4: Section Generation ===")

	if err := a.Phase4GenerateSections(ctx, topic, outline, allSources, config); err != nil {
		slog.Warn("Section generation had errors", "error", err)
	}

	// ========================================
	// PHASE 5: Discover Additional Sections
	// ========================================
	log.Printf("=== PHASE 5: Section Discovery ===")

	discovery, err := a.Phase5DiscoverSections(ctx, topic, outline, allSources)
	if err != nil {
		slog.Warn("Section discovery failed", "error", err)
	} else if len(discovery.SuggestedSections) > 0 {
		// Add discovered sections to outline and generate content
		for _, suggested := range discovery.SuggestedSections {
			newSection := SectionOutline{
				Heading:         suggested.Heading,
				Level:           2,
				Points:          suggested.Points,
				WordTarget:      suggested.WordTarget,
				RelevantSources: suggested.RelevantSources,
			}
			outline.Sections = append(outline.Sections, newSection)

			// Generate content for new section
			relevantSources := selectRelevantSources(&newSection, allSources, config.SourcesPerSection)
			content, err := a.generateSection(ctx, topic, &newSection, relevantSources, "")
			if err != nil {
				slog.Warn("Failed to generate discovered section", "section", suggested.Heading, "error", err)
				continue
			}
			outline.Sections[len(outline.Sections)-1].Content = content
		}
	}

	// ========================================
	// PHASE 6: Integration & Polish
	// ========================================
	log.Printf("=== PHASE 6: Integration ===")

	articleContent, err := a.Phase6IntegrateArticle(ctx, topic, outline)
	if err != nil {
		return fmt.Errorf("article integration failed: %w", err)
	}

	// Save thinking trace if enabled
	if debugSources {
		thinkingPath := fmt.Sprintf("Compendium/_debug/articles/%s/outline.json", slug)
		outlineJSON, _ := json.MarshalIndent(outline, "", "  ")
		if err := a.gh.CreateFile(branchName, thinkingPath, "Add outline for "+topic, string(outlineJSON)); err != nil {
			slog.Warn("Failed to save outline", "error", err)
		}
	}

	// ========================================
	// PHASE 7: Add Citations
	// ========================================
	log.Printf("=== PHASE 7: Citation Addition ===")

	if len(references) > 0 {
		var sourceList strings.Builder
		for i, ref := range references {
			sourceList.WriteString(fmt.Sprintf("[%d] %s\n", i+1, ref))
		}

		citedContent, err := a.llm.AddReferences(ctx, articleContent, sourceList.String())
		if err != nil {
			slog.Warn("Failed to add citations, using uncited content", "error", err)
		} else {
			articleContent = stripCodeFences(citedContent)
			log.Printf("Citations added successfully")
		}

		// Append References section
		articleContent += "\n\n## References\n\n" + strings.Join(references, "\n")
	}

	// ========================================
	// Final: Entity Extraction & Save Article
	// ========================================
	log.Printf("=== Finalizing Article ===")

	return a.finalizeArticle(ctx, topic, category, slug, branchName, articleContent, authMgr)
}

// gatherSources performs initial web research and returns sources and references
func (a *Agent) gatherSources(ctx context.Context, topic, slug, branchName string, authMgr *authority.Manager, progress *ProgressTracker, debugSources bool) ([]SourceInfo, []string, error) {
	// Research - generate multiple search queries for variety
	numQueries := 5
	if envVal := os.Getenv("PHASE1_SEARCH_NUM_QUERIES"); envVal != "" {
		if v, err := strconv.Atoi(envVal); err == nil && v > 0 {
			numQueries = v
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
	}

	type resultWithQuery struct {
		result     search.Result
		queryIndex int
	}

	var results []resultWithQuery
	seenURLs := make(map[string]bool)

	for queryIdx, q := range queries {
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		default:
		}

		res, err := a.search.Search(q)
		if err != nil {
			log.Printf("Search warning for '%s': %v", q, err)
			continue
		}
		log.Printf("Search '%s' returned %d results", q, len(res))
		for _, r := range res {
			if !seenURLs[r.Href] && !strings.HasSuffix(r.Href, ".pdf") {
				results = append(results, resultWithQuery{result: r, queryIndex: queryIdx})
				seenURLs[r.Href] = true
			}
		}

		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}

	// Target sources
	limit := 20
	if envVal := os.Getenv("PHASE1_TARGET_SOURCES"); envVal != "" {
		if v, err := strconv.Atoi(envVal); err == nil && v > 0 {
			limit = v
		}
	}
	log.Printf("Total unique search results: %d (target: %d)", len(results), limit)

	if progress != nil {
		progress.SetTotal(limit)
	}

	// Fetch content in parallel
	type fetchResult struct {
		index   int
		content string
		err     error
	}
	resultsChan := make(chan fetchResult, len(results))
	var wg sync.WaitGroup

	semSize := 3
	if envVal := os.Getenv("SEARCH_MAX_FETCH_CONCURRENCY"); envVal != "" {
		if v, err := strconv.Atoi(envVal); err == nil && v > 0 {
			semSize = v
		}
	}
	sem := make(chan struct{}, semSize)

	for i, r := range results {
		wg.Add(1)
		go func(idx int, urlStr string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			content, err := a.search.FetchContent(urlStr)
			resultsChan <- fetchResult{idx, content, err}
		}(i, r.result.Href)
	}

	fetchedContents := make(map[int]string)
	done := make(chan bool)
	go func() {
		for res := range resultsChan {
			if res.err == nil && len(res.content) >= 100 {
				fetchedContents[res.index] = res.content
			}
		}
		done <- true
	}()

	wg.Wait()
	close(resultsChan)
	<-done

	log.Printf("Successfully fetched %d/%d sources", len(fetchedContents), len(results))

	// Process and summarize sources
	var sources []SourceInfo
	var references []string
	processedCount := 0

	minWords := 200
	if envVal := os.Getenv("SOURCE_SUMMARY_MIN_WORDS"); envVal != "" {
		if v, err := strconv.Atoi(envVal); err == nil && v > 0 {
			minWords = v
		}
	}

	for i, r := range results {
		if processedCount >= limit {
			break
		}

		select {
		case <-ctx.Done():
			return sources, references, ctx.Err()
		default:
		}

		content, ok := fetchedContents[i]
		if !ok {
			continue
		}

		log.Printf("Summarizing source %d/%d: %s", processedCount+1, limit, r.result.Href)
		summary, err := a.llm.SummarizeSource(ctx, topic, r.result.Href, content)
		if err != nil || !summary.Relevant {
			continue
		}

		wordCount := len(strings.Fields(summary.Summary))
		if wordCount < minWords {
			continue
		}

		processedCount++
		if progress != nil {
			progress.Update(processedCount)
		}

		src := SourceInfo{
			Index:   processedCount,
			URL:     r.result.Href,
			Title:   r.result.Title,
			Summary: summary.Summary,
		}
		sources = append(sources, src)
		references = append(references, fmt.Sprintf("[^%d]: [%s](%s)", processedCount, r.result.Title, r.result.Href))

		// Save source summary
		if err := a.saveSourceSummary(ctx, src, topic, slug, branchName, authMgr, debugSources); err != nil {
			slog.Warn("Failed to save source", "url", src.URL, "error", err)
		}
	}

	log.Printf("Phase 1 completed: collected %d sources", len(sources))
	return sources, references, nil
}

// saveSourceSummary saves a source summary to the repository
func (a *Agent) saveSourceSummary(ctx context.Context, src SourceInfo, topic, slug, branchName string, authMgr *authority.Manager, debugSources bool) error {
	u, err := url.Parse(src.URL)
	if err != nil {
		return err
	}

	domain := strings.ReplaceAll(u.Host, ".", "-")
	sourceID := ulid.Make().String()
	sourcePath := fmt.Sprintf("Compendium/_incoming/sources/%s--%s-%d.md", slug, domain, src.Index)

	// Extract entities
	sourceEntities, err := a.llm.ExtractEntities(ctx, src.Summary)
	if err != nil {
		slog.Warn("Entity extraction failed for source", "error", err)
		sourceEntities = nil
	}
	sourceEntities = append(sourceEntities, llm.ExtractedEntity{Name: topic, Type: llm.Topic})

	// Resolve entities
	resolvedSource, err := authMgr.ResolveEntities(sourceEntities)
	if err != nil {
		resolvedSource = make(map[string][]string)
	}

	// Build tags
	var sourceTags []string
	if topicIDs, ok := resolvedSource["topic"]; ok {
		sourceTags = topicIDs
	}
	sourceTagsStr := fmt.Sprintf("[\"%s\"]", strings.Join(sourceTags, "\", \""))

	// Build facets
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

// finalizeArticle extracts entities and saves the final article
func (a *Agent) finalizeArticle(ctx context.Context, topic, category, slug, branchName, content string, authMgr *authority.Manager) error {
	// Extract entities from article
	extracted, err := a.llm.ExtractEntities(ctx, content)
	if err != nil {
		slog.Warn("Entity extraction failed", "error", err)
	}

	// Add category and topic
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
	extracted = append(extracted, llm.ExtractedEntity{Name: topic, Type: llm.Topic})

	resolved, err := authMgr.ResolveEntities(extracted)
	if err != nil {
		slog.Warn("Entity resolution failed", "error", err)
	}

	// Build frontmatter
	id := ulid.Make()
	date := time.Now().UTC().Format("2006-01-02T15:04:05Z")

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

	versionField := fmt.Sprintf("researcher_version: \"%s\"\n", Version)

	var fullContent string
	if strings.HasPrefix(strings.TrimSpace(content), "---") {
		// Article already has frontmatter, inject our fields
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
			injection := fmt.Sprintf("%s\ntags: %s\n%s%s", systemFields, tagsStr, facetsBlock, versionField)
			newLines := append([]string{cleanedLines[0], injection}, cleanedLines[1:]...)
			fullContent = strings.Join(newLines, "\n")
		} else {
			fullContent = content
		}
	} else {
		// Create frontmatter
		frontMatter := fmt.Sprintf(`---
id: %s
title: "%s"
slug: "%s"
created: %s
tags: %s
%s%ssummary: ""
---

`, id, topic, slug, date, tagsStr, facetsBlock, versionField)
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
