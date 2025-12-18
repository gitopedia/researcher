package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"math/rand"
	"path/filepath"
	"strings"
	"time"

	"github.com/gitopedia/researcher/internal/authority"
	"github.com/gitopedia/researcher/internal/github"
	"github.com/gitopedia/researcher/internal/search"
)

type SourceInfo struct {
	Index   int
	URL     string
	Title   string
	Summary string
}

const (
	LabelStatusExpansionDiscovery  = "research:status-expansion-discovery"
	LabelStatusExpansionIntegrated = "research:status-expansion-integrated"

	StepExpansionDiscovery   = "expansion-discovery"
	StepExpansionIntegration = "expansion-integration"
)

// processExistingPRStepByStep builds on an existing draft PR in stages
func (a *Agent) processExistingPRStepByStep(ctx context.Context, pr *github.PRInfo, manualStep string) error {
	prNum := pr.Number
	topic := pr.Title
	if strings.HasPrefix(topic, "Research: ") {
		topic = strings.TrimPrefix(topic, "Research: ")
	}
	slug := strings.ToLower(strings.ReplaceAll(topic, " ", "-"))

	state, err := a.loadState(slug)
	if err != nil {
		// New state for existing PR
		state = &ResearchState{
			Topic:  topic,
			Slug:   slug,
			Branch: pr.HeadBranch,
			Steps:  make(map[string]StepState),
		}
	}

	stepToRun := manualStep
	if stepToRun == "" {
		// Auto-determine next expansion step
		if !strings.Contains(state.LastCompletedStep, "expansion") {
			stepToRun = StepExpansionDiscovery
		} else if state.LastCompletedStep == StepExpansionDiscovery {
			stepToRun = StepExpansionIntegration
		} else {
			// Cyclic expansion: start discovery again
			stepToRun = StepExpansionDiscovery
		}
	}

	// Check if manual review is active and we are not forcing a manual step
	if manualStep == "" {
		hasReview, err := a.gh.HasLabel(prNum, LabelManualReview)
		if err == nil && hasReview {
			log.Printf("PR #%d is waiting for manual review. Skipping.", prNum)
			return nil
		}
	}

	log.Printf("Running expansion step '%s' for PR #%d (%s)", stepToRun, prNum, topic)

	var runErr error
	switch stepToRun {
	case StepExpansionDiscovery:
		runErr = a.stepExpansionDiscovery(ctx, pr, state)
	case StepExpansionIntegration:
		runErr = a.stepExpansionIntegration(ctx, pr, state)
	default:
		return fmt.Errorf("unknown expansion step: %s", stepToRun)
	}

	if runErr != nil {
		return runErr
	}

	state.LastCompletedStep = stepToRun
	state.Steps[stepToRun] = StepState{
		Status:    "completed",
		Timestamp: time.Now(),
	}
	return a.saveState(state)
}

func (a *Agent) stepExpansionDiscovery(ctx context.Context, pr *github.PRInfo, state *ResearchState) error {
	topic := state.Topic
	slug := state.Slug
	branchName := state.Branch

	log.Printf("[Step: Expansion Discovery] Searching for more sources for '%s'...", topic)

	query := topic + " details facts"
	results, err := a.search.Search(query)
	if err != nil {
		return err
	}

	// Save results to _debug folder
	stepDir := fmt.Sprintf("%s/step-expansion-discovery", debugBasePath(slug))
	a.saveDebugJSON(branchName, fmt.Sprintf("%s/results.json", stepDir), "Save expansion discovery results", results)

	comment := fmt.Sprintf("## Expansion Discovery: %s\n\nI found %d potential sources to expand this article. I have saved them to the debug folder for your review:\n`%s` in branch `%s`.\n\nRemove `%s` to proceed with integration.", topic, len(results), stepDir, branchName, LabelManualReview)
	if !a.gh.IsLocal() {
		if err := a.gh.CommentOnPR(pr.Number, comment); err != nil {
			return err
		}
		_ = a.gh.AddLabel(pr.Number, LabelStatusExpansionDiscovery)
		return a.gh.AddLabel(pr.Number, LabelManualReview)
	}
	log.Println(comment)
	return nil
}

func (a *Agent) stepExpansionIntegration(ctx context.Context, pr *github.PRInfo, state *ResearchState) error {
	topic := state.Topic
	slug := state.Slug
	branchName := state.Branch
	log.Printf("[Step: Expansion Integration] Integrating new source into PR #%d...", pr.Number)

	// 1. Find the article file
	files, err := a.gh.ListFilesInBranch(branchName, "Compendium/_incoming/")
	if err != nil {
		return err
	}
	var articlePath string
	for _, f := range files {
		if strings.HasSuffix(f, ".md") && !strings.Contains(f, "/sources/") {
			articlePath = f
			break
		}
	}
	if articlePath == "" {
		return fmt.Errorf("article file not found")
	}

	articleContent, articleSHA, err := a.gh.GetFile(branchName, articlePath)
	if err != nil {
		return err
	}

	// 2. Load discovery results
	stepDirDiscovery := fmt.Sprintf("%s/step-expansion-discovery", debugBasePath(slug))
	content, _, err := a.gh.GetFile(branchName, fmt.Sprintf("%s/results.json", stepDirDiscovery))
	if err != nil {
		return fmt.Errorf("failed to load expansion discovery results: %w", err)
	}

	var results []search.Result
	if err := json.Unmarshal([]byte(content), &results); err != nil {
		return err
	}

	// 3. Fetch and integrate
	for _, r := range results {
		if strings.HasSuffix(r.Href, ".pdf") {
			continue
		}
		content, err := a.search.FetchContent(r.Href)
		if err != nil {
			continue
		}
		mini, err := a.llm.GenerateMiniArticle(ctx, topic, r.Title, content)
		if err != nil {
			continue
		}

		// Save expansion debug info
		stepDir := fmt.Sprintf("%s/step-expansion-integration", debugBasePath(slug))
		a.saveDebugText(branchName, fmt.Sprintf("%s/expansion-summary.md", stepDir), "Save expansion summary", mini)

		// Integrate
		newArticleContent, err := a.llm.IntegrateContent(ctx, topic, articleContent, mini)
		if err != nil {
			continue
		}

		// Update
		if err := a.gh.UpdateFile(branchName, articlePath, "Expand article with new source", newArticleContent, articleSHA); err != nil {
			return err
		}

		// Save Source
		authMgr := authority.NewManager(a.gh)
		srcInfo := SourceInfo{Index: rand.Intn(1000) + 100, URL: r.Href, Title: r.Title, Summary: mini}
		_ = a.saveSourceSummary(ctx, srcInfo, topic, slug, branchName, authMgr, false)

		if !a.gh.IsLocal() {
			_ = a.gh.RemoveLabel(pr.Number, LabelStatusExpansionDiscovery)
			comment := fmt.Sprintf("## Expansion Integrated\n\nIntegrated content from [%s](%s).\n\nThe expansion summary used is available at:\n`%s` in branch `%s`.", r.Title, r.Href, stepDir, branchName)
			_ = a.gh.CommentOnPR(pr.Number, comment)
		}
		return nil
	}

	return fmt.Errorf("no new sources found to integrate")
}

func (a *Agent) processExistingPR(ctx context.Context, pr *github.PRInfo) error {
	log.Printf("Processing Existing PR: #%d (%s)", pr.Number, pr.Title)

	// 1. Checkout Branch
	branchName := pr.HeadBranch

	// 2. Load Article Content
	files, err := a.gh.ListFilesInBranch(branchName, "Compendium/_incoming/")
	if err != nil {
		return fmt.Errorf("failed to list files in branch: %w", err)
	}

	var articlePath string
	for _, f := range files {
		if strings.HasSuffix(f, ".md") && !strings.Contains(f, "/sources/") {
			articlePath = f
			break
		}
	}

	if articlePath == "" {
		return fmt.Errorf("article file not found in branch %s", branchName)
	}

	articleContent, articleSHA, err := a.gh.GetFile(branchName, articlePath)
	if err != nil {
		return fmt.Errorf("failed to get article content: %w", err)
	}

	topic := pr.Title
	if strings.HasPrefix(topic, "Init ") {
		parts := strings.SplitN(topic, ": ", 2)
		if len(parts) == 2 {
			topic = parts[1]
		}
	}

	// 3. Identify Unused Sources
	sourceFiles, err := a.gh.ListFilesInBranch(branchName, "Compendium/_incoming/sources/")
	if err != nil {
		slog.Warn("Failed to list sources", "error", err)
	}

	var unusedSources []string
	for _, f := range sourceFiles {
		if !strings.HasSuffix(f, ".md") {
			continue
		}

		srcContent, _, err := a.gh.GetFile(branchName, f)
		if err == nil {
			if !strings.Contains(srcContent, "used_in_version:") {
				unusedSources = append(unusedSources, f)
			}
		}
	}

	log.Printf("Found %d unused sources", len(unusedSources))

	// 4. Decide Path
	if len(unusedSources) > 0 {
		srcPath := unusedSources[rand.Intn(len(unusedSources))]
		return a.integrateSource(ctx, topic, branchName, articlePath, articleContent, articleSHA, srcPath)
	}

	// Path B: Fetch New Source
	return a.fetchNewSource(ctx, topic, branchName, articlePath, articleContent)
}

func (a *Agent) integrateSource(ctx context.Context, topic, branchName, articlePath, articleContent, articleSHA, srcPath string) error {
	log.Printf("Integrating source: %s", srcPath)

	srcContent, srcSHA, err := a.gh.GetFile(branchName, srcPath)
	if err != nil {
		return err
	}

	srcBody := srcContent
	if strings.HasPrefix(srcBody, "---") {
		parts := strings.SplitN(srcBody, "---", 3)
		if len(parts) == 3 {
			srcBody = parts[2]
		}
	}

	log.Println("Checking relevance...")
	resultRelevance, err := a.llm.CheckRelevance(ctx, topic, srcBody)
	if err != nil {
		return fmt.Errorf("relevance check failed: %w", err)
	}
	if !resultRelevance.Relevant {
		return a.markSourceAsUsed(branchName, srcPath, srcContent, srcSHA, "rejected: "+resultRelevance.Reason)
	}

	log.Println("Checking redundancy...")
	resultRedundancy, err := a.llm.CheckRedundancy(ctx, topic, articleContent, srcBody)
	if err == nil && resultRedundancy.IsRedundant {
		return a.markSourceAsUsed(branchName, srcPath, srcContent, srcSHA, "redundant")
	}

	log.Println("Integrating content...")
	newArticleContent, err := a.llm.IntegrateContent(ctx, topic, articleContent, srcBody)
	if err != nil {
		return fmt.Errorf("integration failed: %w", err)
	}

	if err := a.gh.UpdateFile(branchName, articlePath, "Update article with new source", newArticleContent, articleSHA); err != nil {
		return fmt.Errorf("failed to update article: %w", err)
	}

	return a.markSourceAsUsed(branchName, srcPath, srcContent, srcSHA, "integrated")
}

func (a *Agent) markSourceAsUsed(branchName, srcPath, content, sha, status string) error {
	lines := strings.Split(content, "\n")
	var newLines []string
	inFM := false
	inserted := false

	for _, line := range lines {
		if line == "---" {
			if !inFM {
				inFM = true
			} else {
				if !inserted {
					newLines = append(newLines, fmt.Sprintf("used_status: \"%s\"", status))
					newLines = append(newLines, fmt.Sprintf("used_at: %s", time.Now().Format(time.RFC3339)))
					inserted = true
				}
				inFM = false
			}
		}
		newLines = append(newLines, line)
	}

	newContent := strings.Join(newLines, "\n")
	return a.gh.UpdateFile(branchName, srcPath, "Mark source as used: "+status, newContent, sha)
}

func (a *Agent) fetchNewSource(ctx context.Context, topic, branchName, articlePath, articleContent string) error {
	log.Printf("Fetching new source for: %s", topic)
	query := topic + " details facts"
	results, err := a.search.Search(query)
	if err != nil {
		return err
	}

	for _, r := range results {
		if strings.HasSuffix(r.Href, ".pdf") {
			continue
		}

		content, err := a.search.FetchContent(r.Href)
		if err != nil {
			continue
		}

		mini, err := a.llm.GenerateMiniArticle(ctx, topic, r.Title, content)
		if err != nil {
			continue
		}

		authMgr := authority.NewManager(a.gh)
		srcInfo := SourceInfo{
			Index:   rand.Intn(1000) + 100,
			URL:     r.Href,
			Title:   r.Title,
			Summary: mini,
		}

		slug := strings.TrimSuffix(filepath.Base(articlePath), ".md")
		if err := a.saveSourceSummary(ctx, srcInfo, topic, slug, branchName, authMgr, false); err != nil {
			continue
		}

		return nil
	}

	return fmt.Errorf("no new sources found")
}
