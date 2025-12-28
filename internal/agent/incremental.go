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
	"github.com/gitopedia/researcher/internal/search"
	gh "github.com/google/go-github/v57/github"
	"github.com/oklog/ulid/v2"
)

const (
	StepDiscovery     = "discovery"
	StepSummarization = "summarization"
	StepDrafting      = "drafting"
	StepFinalize      = "finalize"
)

type ResearchState struct {
	Topic             string               `json:"topic"`
	Slug              string               `json:"slug"`
	Branch            string               `json:"branch"`
	LastCompletedStep string               `json:"last_completed_step"`
	Steps             map[string]StepState `json:"steps"`
}

type StepState struct {
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
}

// ImprovementResult tracks the outcome of an article improvement attempt
type ImprovementResult struct {
	ArticleName    string
	Mode           string // "Add New Section" or "Improve Existing Section"
	Success        bool
	SectionName    string // Section added or improved
	SourceTitle    string
	SourceURL      string
	Score          int    // For Mode B improvements
	ErrorMessage   string
	SkippedSources []string // Encyclopedia sources that were skipped
}

func (a *Agent) loadState(slug string) (*ResearchState, error) {
	statePath := fmt.Sprintf("%s/state.json", debugBasePath(slug))
	content, _, err := a.gh.GetFile("", statePath)
	if err != nil {
		return nil, err
	}
	var state ResearchState
	if err := json.Unmarshal([]byte(content), &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func (a *Agent) saveState(state *ResearchState) error {
	statePath := fmt.Sprintf("%s/state.json", debugBasePath(state.Slug))
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return a.gh.CreateFile(state.Branch, statePath, "Update research state", string(data))
}

// processNewTopicStepByStep handles the creation of a new research topic in stages.
func (a *Agent) processNewTopicStepByStep(ctx context.Context, issue *gh.Issue, manualStep string) error {
	title := *issue.Title
	topic := cleanTopic(title)
	slug := strings.ToLower(strings.ReplaceAll(topic, " ", "-"))

	state, err := a.loadState(slug)
	if err != nil {
		// New state
		state = &ResearchState{
			Topic: topic,
			Slug:  slug,
			Steps: make(map[string]StepState),
		}
	}

	stepToRun := manualStep
	if stepToRun == "" {
		// Auto-determine next step if not manual
		switch state.LastCompletedStep {
		case "":
			stepToRun = StepDiscovery
		case StepDiscovery:
			stepToRun = StepSummarization
		case StepSummarization:
			stepToRun = StepDrafting
		case StepDrafting:
			stepToRun = StepFinalize
		default:
			log.Printf("All steps completed for %s", topic)
			return nil
		}
	}

	log.Printf("Running step '%s' for topic '%s'", stepToRun, topic)

	var runErr error
	switch stepToRun {
	case StepDiscovery:
		runErr = a.stepDiscovery(ctx, issue, state)
	case StepSummarization:
		runErr = a.stepSummarization(ctx, issue, state)
	case StepDrafting:
		runErr = a.stepDrafting(ctx, issue, state)
	case StepFinalize:
		runErr = a.stepFinalize(ctx, issue, state)
	default:
		return fmt.Errorf("unknown step: %s", stepToRun)
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

func (a *Agent) stepDiscovery(ctx context.Context, issue *gh.Issue, state *ResearchState) error {
	topic := state.Topic
	slug := state.Slug
	log.Printf("[Step: Discovery] Searching for sources for '%s'...", topic)

	if state.Branch == "" {
		state.Branch = fmt.Sprintf("research/%s", slug)
	}

	// Create branch immediately
	if err := a.gh.CreateBranch("main", state.Branch); err != nil {
		log.Printf("Branch %s might already exist, continuing...", state.Branch)
	}

	query := topic + " encyclopedia overview"
	results, err := a.search.Search(query)
	if err != nil {
		return err
	}

	// Save results to _debug folder
	stepDir := fmt.Sprintf("%s/step-1-discovery", debugBasePath(slug))
	a.saveDebugJSON(state.Branch, fmt.Sprintf("%s/results.json", stepDir), "Save discovery results", results)

	comment := fmt.Sprintf("## Research Discovery: %s\n\nI found %d potential sources. I have saved them to the debug folder for your review:\n`%s` in branch `%s`.", topic, len(results), stepDir, state.Branch)
	if err := a.gh.CommentOnIssue(*issue.Number, comment); err != nil {
		return err
	}
	log.Println(comment)
	return nil
}

func (a *Agent) stepSummarization(ctx context.Context, issue *gh.Issue, state *ResearchState) error {
	topic := state.Topic
	slug := state.Slug
	branchName := state.Branch
	log.Printf("[Step: Summarization] Summarizing source for '%s'...", topic)

	// Pick the first source from discovery results saved in branch
	stepDirDiscovery := fmt.Sprintf("%s/step-1-discovery", debugBasePath(slug))
	content, _, err := a.gh.GetFile(branchName, fmt.Sprintf("%s/results.json", stepDirDiscovery))
	if err != nil {
		return fmt.Errorf("failed to load discovery results: %w", err)
	}

	var results []search.Result
	if err := json.Unmarshal([]byte(content), &results); err != nil {
		return err
	}

	var sourceInfo SourceInfo
	found := false
	for _, r := range results {
		if strings.HasSuffix(r.Href, ".pdf") {
			log.Printf("Skipping PDF source: %s", r.Href)
			continue
		}

		// Check if source is an encyclopedia
		domain := extractDomain(r.Href)
		encCheck, err := a.llm.IsEncyclopediaSource(ctx, domain, r.Href, r.Title)
		if err != nil {
			log.Printf("Failed to check encyclopedia status for %s: %v", domain, err)
		} else if encCheck.IsEncyclopedia {
			log.Printf("Skipping encyclopedia source: %s (%s)", domain, encCheck.Reason)
			continue
		}

		content, err := a.search.FetchContent(r.Href)
		if err != nil {
			log.Printf("Failed to fetch content from %s: %v", r.Href, err)
			continue
		}
		summary, err := a.llm.SummarizeSource(ctx, topic, r.Href, content)
		if err != nil {
			log.Printf("Failed to summarize source %s: %v", r.Href, err)
			continue
		}
		if !summary.Relevant {
			log.Printf("Source rejected: %s - Reason: %s", r.Href, summary.Reason)
			continue
		}
		log.Printf("Found relevant source: %s", r.Href)
		sourceInfo = SourceInfo{Index: 1, URL: r.Href, Title: r.Title, Summary: summary.Summary}
		found = true
		break
	}

	if !found {
		return fmt.Errorf("no relevant non-encyclopedia sources found during summarization step")
	}

	authMgr := authority.NewManager(a.gh)
	_ = authMgr.Load("main")

	if err := a.saveSourceSummary(ctx, sourceInfo, topic, slug, branchName, authMgr, false); err != nil {
		return err
	}

	// Save summary to _debug folder
	stepDir := fmt.Sprintf("%s/step-2-summarization", debugBasePath(slug))
	a.saveDebugText(branchName, fmt.Sprintf("%s/summary-1.md", stepDir), "Save source summary", sourceInfo.Summary)

	comment := fmt.Sprintf("## Research Summarization\n\nI have summarized the source: [%s](%s).\n\nThe full summary is available in the debug folder:\n`%s` in branch `%s`.", sourceInfo.Title, sourceInfo.URL, stepDir, branchName)
	if err := a.gh.CommentOnIssue(*issue.Number, comment); err != nil {
		return err
	}
	log.Println(comment)
	return nil
}

func (a *Agent) stepDrafting(ctx context.Context, issue *gh.Issue, state *ResearchState) error {
	topic := state.Topic
	slug := state.Slug
	branchName := state.Branch
	log.Printf("[Step: Drafting] Generating article for '%s'...", topic)

	// Find the summarized source
	stepDirSummary := fmt.Sprintf("%s/step-2-summarization", debugBasePath(slug))
	summaryPath := fmt.Sprintf("%s/summary-1.md", stepDirSummary)
	summary, _, err := a.gh.GetFile(branchName, summaryPath)
	if err != nil {
		return fmt.Errorf("failed to load summary from %s: %w", summaryPath, err)
	}

	if len(summary) == 0 {
		return fmt.Errorf("summary file is empty: %s", summaryPath)
	}

	log.Printf("Loaded summary from %s: %d characters", summaryPath, len(summary))
	miniArticle, err := a.llm.GenerateMiniArticle(ctx, topic, "Source", summary)
	if err != nil {
		return err
	}

	// Create Article File
	id := ulid.Make()
	date := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	frontMatter := fmt.Sprintf("---\nid: %s\ntitle: \"%s\"\nslug: \"%s\"\ncreated: %s\nresearcher_version: \"1\"\niterations: 0\n---\n\n", id, topic, slug, date)
	fullContent := frontMatter + miniArticle

	articlePath := fmt.Sprintf("Compendium/_incoming/%s.md", slug)
	if err := a.gh.CreateFile(branchName, articlePath, "Draft article", fullContent); err != nil {
		return err
	}

	// Save drafting results
	stepDir := fmt.Sprintf("%s/step-3-drafting", debugBasePath(slug))
	a.saveDebugText(branchName, fmt.Sprintf("%s/article-draft.md", stepDir), "Save drafted article", fullContent)

	comment := fmt.Sprintf("## Research Drafting\n\nI have drafted the article. You can see it in branch `%s`.", branchName)
	_ = a.gh.CommentOnIssue(*issue.Number, comment)
	log.Println(comment)
	return nil
}

func (a *Agent) stepFinalize(ctx context.Context, issue *gh.Issue, state *ResearchState) error {
	topic := state.Topic
	branchName := state.Branch
	log.Printf("[Step: Finalize] Creating PR for '%s'...", topic)

	// TODO: Implement push and PR creation
	log.Printf("Research complete. Push branch '%s' and create PR manually.", branchName)
	return nil
}

func cleanTopic(title string) string {
	topic := strings.TrimSpace(strings.TrimPrefix(title, "Category:"))
	if topic == title {
		topic = strings.TrimSpace(strings.TrimPrefix(title, "Research:"))
	}
	return topic
}

// processNewTopic handles the creation of a new research topic from scratch.
func (a *Agent) processNewTopic(ctx context.Context, issue *gh.Issue) error {
	title := *issue.Title
	topic := cleanTopic(title)

	log.Printf("Starting NEW TOPIC flow for Issue #%d: '%s'", *issue.Number, topic)

	slug := strings.ToLower(strings.ReplaceAll(topic, " ", "-"))
	branchName := fmt.Sprintf("research/%s-%s", slug, time.Now().Format("20060102-150405"))

	// 1. Create Branch
	log.Printf("Creating branch %s...", branchName)
	if err := a.gh.CreateBranch("main", branchName); err != nil {
		return fmt.Errorf("failed to create branch: %w", err)
	}

	// 2. Search for 1 Source
	query := topic + " encyclopedia overview"
	log.Printf("Searching for: %s", query)
	results, err := a.search.Search(query)
	if err != nil {
		return fmt.Errorf("search failed: %w", err)
	}

	var sourceInfo SourceInfo
	found := false

	// Load authorities for entity resolution
	authMgr := authority.NewManager(a.gh)
	if err := authMgr.Load("main"); err != nil {
		slog.Warn("Failed to load authorities", "error", err)
	}

	for _, r := range results {
		if strings.HasSuffix(r.Href, ".pdf") {
			continue
		}

		// Check if source is an encyclopedia
		domain := extractDomain(r.Href)
		encCheck, err := a.llm.IsEncyclopediaSource(ctx, domain, r.Href, r.Title)
		if err != nil {
			log.Printf("Failed to check encyclopedia status for %s: %v", domain, err)
		} else if encCheck.IsEncyclopedia {
			log.Printf("Skipping encyclopedia source: %s (%s)", domain, encCheck.Reason)
			continue
		}

		log.Printf("Checking source: %s", r.Href)
		content, err := a.search.FetchContent(r.Href)
		if err != nil {
			continue
		}

		summary, err := a.llm.SummarizeSource(ctx, topic, r.Href, content)
		if err != nil {
			continue
		}

		if summary.Relevant {
			sourceInfo = SourceInfo{
				Index:   1,
				URL:     r.Href,
				Title:   r.Title,
				Summary: summary.Summary,
			}
			found = true
			log.Printf("Found relevant source: %s", r.Title)
			break
		}
	}

	if !found {
		return fmt.Errorf("could not find a relevant non-encyclopedia source for %s", topic)
	}

	// 3. Save Source
	if err := a.saveSourceSummary(ctx, sourceInfo, topic, slug, branchName, authMgr, false); err != nil {
		return fmt.Errorf("failed to save source: %w", err)
	}

	// 4. Generate Mini Article (Overview)
	log.Printf("Generating mini-article from source...")
	miniArticle, err := a.llm.GenerateMiniArticle(ctx, topic, sourceInfo.Title, sourceInfo.Summary)
	if err != nil {
		return fmt.Errorf("failed to generate mini article: %w", err)
	}

	// 5. Create Article File
	id := ulid.Make()
	date := time.Now().UTC().Format("2006-01-02T15:04:05Z")

	// Extract entities
	extracted, err := a.llm.ExtractEntities(ctx, miniArticle)
	if err != nil {
		slog.Warn("Entity extraction failed", "error", err)
	}
	extracted = append(extracted, llm.ExtractedEntity{Name: topic, Type: llm.Topic})

	resolved, err := authMgr.ResolveEntities(extracted)
	if err != nil {
		slog.Warn("Entity resolution failed", "error", err)
	}

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

	frontMatter := fmt.Sprintf(`---
id: %s
title: "%s"
slug: "%s"
created: %s
tags: %s
%sresearcher_version: "1"
model: "%s"
iterations: 0
summary: "Initial overview based on %s"
---

`, id, topic, slug, date, tagsStr, facetsBlock, os.Getenv("LLM_MODEL_ARTICLE"), sourceInfo.Title)

	fullContent := frontMatter + miniArticle + fmt.Sprintf("\n\n## References\n\n[^1]: [%s](%s)", sourceInfo.Title, sourceInfo.URL)

	articlePath := fmt.Sprintf("Compendium/_incoming/%s.md", slug)
	if err := a.gh.CreateFile(branchName, articlePath, fmt.Sprintf("Init article: %s", topic), fullContent); err != nil {
		return fmt.Errorf("failed to create article file: %w", err)
	}

	// TODO: Implement push and PR creation
	log.Printf("Research complete. Push branch '%s' and create PR manually.", branchName)
	return nil
}

// getEnvInt reads an integer from an environment variable with a default fallback
func getEnvInt(key string, defaultVal int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil && i > 0 {
			return i
		}
	}
	return defaultVal
}

// getEnvBool reads a boolean from an environment variable with a default fallback
func getEnvBool(key string, defaultVal bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	v = strings.ToLower(v)
	return v == "true" || v == "1" || v == "yes"
}

// processTopicWithIterations handles a claimed topic issue by iterating through articles
// It processes N articles (creating new or improving existing), then creates a PR
func (a *Agent) processTopicWithIterations(ctx context.Context, issue *gh.Issue, botUsername string) error {
	iterations := getEnvInt("TOPIC_PROCESSING_ITERATIONS", 10)
	issueNum := *issue.Number
	topicTitle := issue.GetTitle()

	log.Printf("Starting iterative processing for topic #%d: %s (%d iterations)", issueNum, topicTitle, iterations)

	// Create branch once for all iterations
	branchName := fmt.Sprintf("research/topic-%d-%s", issueNum, time.Now().Format("20060102-150405"))
	log.Printf("Creating branch %s...", branchName)
	if err := a.gh.CreateBranch("main", branchName); err != nil {
		return fmt.Errorf("failed to create branch: %w", err)
	}

	startTime := time.Now()

	// Track actions for summary
	var articlesCreated []string
	var improvementResults []*ImprovementResult
	var errors []string

	// Track failed sources to avoid retrying them in this run
	failedSources := make(map[string]bool)

	// Process articles in iterations
	for i := 0; i < iterations; i++ {
		log.Printf("=== Topic #%d iteration %d/%d ===", issueNum, i+1, iterations)

		// Refresh article list from issue (it may have been updated)
		latestIssue, err := a.gh.GetIssue(issueNum)
		if err != nil {
			slog.Warn("Failed to refresh issue", "issue", issueNum, "error", err)
			errors = append(errors, fmt.Sprintf("Failed to refresh issue: %v", err))
			continue
		}

		articles := github.ParseArticlesFromBody(latestIssue.GetBody())
		if len(articles) == 0 {
			log.Printf("No articles found in topic issue #%d, skipping iteration", issueNum)
			continue
		}

		// Random selection
		rand.Seed(time.Now().UnixNano())
		article := articles[rand.Intn(len(articles))]

		if !article.Completed {
			// Create new article
			log.Printf("Processing NEW article: '%s'", article.Name)
			if err := a.processNewArticle(ctx, issue, article.Name, branchName, failedSources); err != nil {
				slog.Warn("Failed to create article", "article", article.Name, "error", err)
				errors = append(errors, fmt.Sprintf("Failed to create '%s': %v", article.Name, err))
				continue
			}

			articlesCreated = append(articlesCreated, article.Name)

			// Mark as completed in issue
			if err := a.checkOffArticle(issueNum, article.Name); err != nil {
				slog.Warn("Failed to check off article", "article", article.Name, "error", err)
			}
		} else {
			// Improve existing article
			log.Printf("Processing EXISTING article for improvement: '%s'", article.Name)
			result, err := a.improveArticle(ctx, issue, article.Name, branchName, failedSources)
			if err != nil {
				slog.Warn("Failed to improve article", "article", article.Name, "error", err)
				errors = append(errors, fmt.Sprintf("Failed to improve '%s': %v", article.Name, err))
			}
			if result != nil {
				improvementResults = append(improvementResults, result)
			}
		}
	}

	log.Printf("Completed %d iterations for topic #%d", iterations, issueNum)

	// Build comprehensive summary
	var summaryBuilder strings.Builder
	summaryBuilder.WriteString("## 📊 Research Bot Summary\n\n")
	summaryBuilder.WriteString(fmt.Sprintf("- **Branch:** `%s`\n", branchName))
	summaryBuilder.WriteString(fmt.Sprintf("- **Duration:** %s\n", time.Since(startTime).Round(time.Second)))
	summaryBuilder.WriteString(fmt.Sprintf("- **Iterations:** %d\n\n", iterations))

	// Articles created
	if len(articlesCreated) > 0 {
		summaryBuilder.WriteString(fmt.Sprintf("### ✅ Articles Created (%d)\n", len(articlesCreated)))
		for _, a := range articlesCreated {
			summaryBuilder.WriteString(fmt.Sprintf("- %s\n", a))
		}
		summaryBuilder.WriteString("\n")
	}

	// Articles improved - with details
	successfulImprovements := []*ImprovementResult{}
	for _, r := range improvementResults {
		if r.Success {
			successfulImprovements = append(successfulImprovements, r)
		}
	}

	if len(successfulImprovements) > 0 {
		summaryBuilder.WriteString(fmt.Sprintf("### 📝 Articles Improved (%d)\n", len(successfulImprovements)))
		for _, r := range successfulImprovements {
			if r.Mode == "Add New Section" {
				summaryBuilder.WriteString(fmt.Sprintf("- **%s**: Added section \"%s\"", r.ArticleName, r.SectionName))
			} else {
				summaryBuilder.WriteString(fmt.Sprintf("- **%s**: Improved section \"%s\" (score: %d/10)", r.ArticleName, r.SectionName, r.Score))
			}
			if r.SourceTitle != "" {
				summaryBuilder.WriteString(fmt.Sprintf(" — [%s](%s)", r.SourceTitle, r.SourceURL))
			}
			summaryBuilder.WriteString("\n")
		}
		summaryBuilder.WriteString("\n")
	}

	// Errors
	if len(errors) > 0 {
		summaryBuilder.WriteString(fmt.Sprintf("### ⚠️ Errors (%d)\n", len(errors)))
		for _, e := range errors {
			summaryBuilder.WriteString(fmt.Sprintf("- %s\n", e))
		}
		summaryBuilder.WriteString("\n")
	}

	// No activity
	if len(articlesCreated) == 0 && len(successfulImprovements) == 0 {
		summaryBuilder.WriteString("No articles were processed in this run.\n")
	}

	// Post single summary comment
	if err := a.gh.CommentOnIssue(issueNum, summaryBuilder.String()); err != nil {
		slog.Warn("Failed to post summary comment", "issue", issueNum, "error", err)
	}

	// Check if PR creation is enabled
	createPR := getEnvBool("CREATE_PR_AFTER_ITERATIONS", false)
	if !createPR {
		log.Printf("PR creation disabled (CREATE_PR_AFTER_ITERATIONS=false). Branch '%s' ready for manual review.", branchName)
		// Still unassign the bot so the topic can be picked up again if needed
		if botUsername != "" {
			if err := a.gh.RemoveAssignees(issueNum, []string{botUsername}); err != nil {
				slog.Warn("Failed to unassign after completion", "issue", issueNum, "error", err)
			} else {
				log.Printf("Unassigned bot from issue #%d", issueNum)
			}
		}
		return nil
	}

	// Create PR
	prTitle := fmt.Sprintf("Research: %s", topicTitle)
	prBody := fmt.Sprintf("Automated research for topic issue #%d: %s\n\nCloses #%d", issueNum, topicTitle, issueNum)
	pr, err := a.gh.CreatePullRequest(prTitle, prBody, branchName, "main")
	if err != nil {
		slog.Error("Failed to create PR", "error", err)
		return fmt.Errorf("failed to create PR: %w", err)
	}
	log.Printf("Created PR #%d: %s", pr.GetNumber(), prTitle)

	// Add "pending review" label to the issue
	if err := a.gh.AddLabel(issueNum, LabelPendingReview); err != nil {
		slog.Warn("Failed to add pending review label", "issue", issueNum, "error", err)
	} else {
		log.Printf("Added '%s' label to issue #%d", LabelPendingReview, issueNum)
	}

	// Unassign bot from the issue
	if botUsername != "" {
		if err := a.gh.RemoveAssignees(issueNum, []string{botUsername}); err != nil {
			slog.Warn("Failed to unassign after completion", "issue", issueNum, "error", err)
		} else {
			log.Printf("Unassigned bot from issue #%d", issueNum)
		}
	}

	return nil
}

// checkOffArticle marks an article as completed in the topic issue's body
func (a *Agent) checkOffArticle(issueNum int, articleName string) error {
	latestIssue, err := a.gh.GetIssue(issueNum)
	if err != nil {
		return fmt.Errorf("failed to re-fetch issue for checkbox update: %w", err)
	}

	newBody := github.CheckArticleInBody(latestIssue.GetBody(), articleName)
	if err := a.gh.UpdateIssueBody(issueNum, newBody); err != nil {
		return fmt.Errorf("failed to update issue body: %w", err)
	}

	log.Printf("Checked off article '%s' in issue #%d", articleName, issueNum)
	return nil
}

// processNewArticle creates a new article for the given article name using a shared branch
// This is similar to processNewTopic but uses a pre-created branch and explicit article name
func (a *Agent) processNewArticle(ctx context.Context, issue *gh.Issue, articleName, branchName string, failedSources map[string]bool) error {
	topic := cleanTopic(articleName)
	issueNum := *issue.Number

	log.Printf("Creating NEW article '%s' for Issue #%d", topic, issueNum)

	slug := strings.ToLower(strings.ReplaceAll(topic, " ", "-"))

	// Search for sources
	query := topic + " encyclopedia overview"
	log.Printf("Searching for: %s", query)
	results, err := a.search.Search(query)
	if err != nil {
		return fmt.Errorf("search failed: %w", err)
	}

	var sourceInfo SourceInfo
	found := false

	// Load authorities for entity resolution
	authMgr := authority.NewManager(a.gh)
	if err := authMgr.Load("main"); err != nil {
		slog.Warn("Failed to load authorities", "error", err)
	}

	for _, r := range results {
		if strings.HasSuffix(r.Href, ".pdf") {
			continue
		}

		// Skip sources that have already failed in this run
		if failedSources[r.Href] {
			log.Printf("Skipping previously failed source: %s", r.Href)
			continue
		}

		// Check if source is an encyclopedia
		domain := extractDomain(r.Href)
		encCheck, err := a.llm.IsEncyclopediaSource(ctx, domain, r.Href, r.Title)
		if err != nil {
			log.Printf("Failed to check encyclopedia status for %s: %v", domain, err)
		} else if encCheck.IsEncyclopedia {
			log.Printf("Skipping encyclopedia source: %s (%s)", domain, encCheck.Reason)
			continue
		}

		log.Printf("Checking source: %s", r.Href)
		content, err := a.search.FetchContent(r.Href)
		if err != nil {
			continue
		}

		summary, err := a.llm.SummarizeSource(ctx, topic, r.Href, content)
		if err != nil {
			continue
		}

		if summary.Relevant {
			sourceInfo = SourceInfo{
				Index:   1,
				URL:     r.Href,
				Title:   r.Title,
				Summary: summary.Summary,
			}
			found = true
			log.Printf("Found relevant source: %s", r.Title)
			break
		}
	}

	if !found {
		return fmt.Errorf("could not find a relevant non-encyclopedia source for %s", topic)
	}

	// Save Source
	if err := a.saveSourceSummary(ctx, sourceInfo, topic, slug, branchName, authMgr, false); err != nil {
		return fmt.Errorf("failed to save source: %w", err)
	}

	// Generate Mini Article (Overview)
	log.Printf("Generating mini-article from source...")
	miniArticle, err := a.llm.GenerateMiniArticle(ctx, topic, sourceInfo.Title, sourceInfo.Summary)
	if err != nil {
		// Mark this source as failed so we don't retry it
		failedSources[sourceInfo.URL] = true
		log.Printf("Marking source as failed: %s", sourceInfo.URL)
		return fmt.Errorf("failed to generate mini article: %w", err)
	}

	// Create Article File
	id := ulid.Make()
	date := time.Now().UTC().Format("2006-01-02T15:04:05Z")

	// Extract entities
	extracted, err := a.llm.ExtractEntities(ctx, miniArticle)
	if err != nil {
		slog.Warn("Entity extraction failed", "error", err)
	}
	extracted = append(extracted, llm.ExtractedEntity{Name: topic, Type: llm.Topic})

	resolved, err := authMgr.ResolveEntities(extracted)
	if err != nil {
		slog.Warn("Entity resolution failed", "error", err)
	}

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

	frontMatter := fmt.Sprintf(`---
id: %s
title: "%s"
slug: "%s"
created: %s
tags: %s
%sresearcher_version: "1"
model: "%s"
iterations: 0
summary: "Initial overview based on %s"
---

`, id, topic, slug, date, tagsStr, facetsBlock, os.Getenv("LLM_MODEL_ARTICLE"), sourceInfo.Title)

	fullContent := frontMatter + miniArticle + fmt.Sprintf("\n\n## References\n\n[^1]: [%s](%s)", sourceInfo.Title, sourceInfo.URL)

	articlePath := fmt.Sprintf("Compendium/_incoming/%s.md", slug)
	if err := a.gh.CreateFile(branchName, articlePath, fmt.Sprintf("Add article: %s", topic), fullContent); err != nil {
		return fmt.Errorf("failed to create article file: %w", err)
	}

	log.Printf("Successfully created article '%s'", topic)
	return nil
}

// improveArticle improves an existing article using one of two modes:
// Mode A: Find a new source, create temp article, compare sections, add new section if valuable
// Mode B: Select an existing section, search for more details, improve that section
func (a *Agent) improveArticle(ctx context.Context, issue *gh.Issue, articleName, branchName string, failedSources map[string]bool) (*ImprovementResult, error) {
	topic := cleanTopic(articleName)
	slug := strings.ToLower(strings.ReplaceAll(topic, " ", "-"))

	result := &ImprovementResult{
		ArticleName:    articleName,
		SkippedSources: []string{},
	}

	log.Printf("[Improvement] Starting improvement for article '%s' on branch '%s'", topic, branchName)

	// Load existing article
	articlePath := fmt.Sprintf("Compendium/_incoming/%s.md", slug)
	articleContent, articleSHA, err := a.gh.GetFile(branchName, articlePath)
	if err != nil {
		// Try to find the article on main branch
		articleContent, articleSHA, err = a.gh.GetFile("main", articlePath)
		if err != nil {
			result.ErrorMessage = fmt.Sprintf("failed to load article %s: %v", articlePath, err)
			return result, fmt.Errorf("failed to load article %s: %w", articlePath, err)
		}
	}

	// Extract sections from existing article
	existingSections, err := a.llm.ExtractSections(ctx, articleContent)
	if err != nil {
		log.Printf("[Improvement] Warning: Failed to extract sections: %v", err)
		existingSections = nil
	}

	// Randomly choose between Mode A (add new section) and Mode B (improve existing section)
	rand.Seed(time.Now().UnixNano())
	useAddNewSection := rand.Intn(2) == 0

	if useAddNewSection {
		result.Mode = "Add New Section"
	} else {
		result.Mode = "Improve Existing Section"
	}

	var actionLog strings.Builder
	actionLog.WriteString(fmt.Sprintf("## Improvement Attempt: %s\n\n", topic))
	actionLog.WriteString(fmt.Sprintf("- **Mode:** %s\n", result.Mode))

	if useAddNewSection {
		err = a.improveModeAddSection(ctx, topic, slug, branchName, articlePath, articleContent, articleSHA, existingSections, &actionLog, result, failedSources)
	} else {
		err = a.improveModeImproveSection(ctx, topic, slug, branchName, articlePath, articleContent, articleSHA, existingSections, &actionLog, result, failedSources)
	}

	// Save action log to debug folder
	debugPath := fmt.Sprintf("%s/improvement-log-%s.md", debugBasePath(slug), time.Now().Format("20060102-150405"))
	a.saveDebugText(branchName, debugPath, "Save improvement log", actionLog.String())

	if err != nil {
		result.ErrorMessage = err.Error()
		return result, err
	}

	result.Success = true
	return result, nil
}

// improveModeAddSection implements Mode A: Find new source, create temp article, add new section
func (a *Agent) improveModeAddSection(ctx context.Context, topic, slug, branchName, articlePath, articleContent, articleSHA string, existingSections []llm.ArticleSection, actionLog *strings.Builder, result *ImprovementResult, failedSources map[string]bool) error {
	log.Printf("[Mode A] Searching for new source for topic '%s'", topic)
	actionLog.WriteString("\n### Search for New Source\n\n")

	// Search for a new source (excluding encyclopedias)
	query := topic + " details facts"
	results, err := a.search.Search(query)
	if err != nil {
		actionLog.WriteString(fmt.Sprintf("- **Error:** Search failed: %v\n", err))
		return fmt.Errorf("search failed: %w", err)
	}

	actionLog.WriteString(fmt.Sprintf("- Found %d search results\n", len(results)))

	var sourceInfo SourceInfo
	found := false

	for _, r := range results {
		if strings.HasSuffix(r.Href, ".pdf") {
			continue
		}

		// Skip sources that have already failed in this run
		if failedSources[r.Href] {
			log.Printf("[Mode A] Skipping previously failed source: %s", r.Href)
			actionLog.WriteString(fmt.Sprintf("- Skipped previously failed: %s\n", r.Href))
			continue
		}

		// Check if source is an encyclopedia
		domain := extractDomain(r.Href)
		encCheck, err := a.llm.IsEncyclopediaSource(ctx, domain, r.Href, r.Title)
		if err != nil {
			log.Printf("[Mode A] Failed to check encyclopedia status for %s: %v", domain, err)
		} else if encCheck.IsEncyclopedia {
			log.Printf("[Mode A] Skipping encyclopedia source: %s (%s)", domain, encCheck.Reason)
			actionLog.WriteString(fmt.Sprintf("- Skipped encyclopedia: %s (%s)\n", domain, encCheck.Reason))
			result.SkippedSources = append(result.SkippedSources, domain)
			continue
		}

		log.Printf("[Mode A] Fetching content from: %s", r.Href)
		content, err := a.search.FetchContent(r.Href)
		if err != nil {
			log.Printf("[Mode A] Failed to fetch: %v", err)
			continue
		}

		summary, err := a.llm.SummarizeSource(ctx, topic, r.Href, content)
		if err != nil {
			log.Printf("[Mode A] Failed to summarize: %v", err)
			continue
		}

		if summary.Relevant {
			sourceInfo = SourceInfo{
				Index:   rand.Intn(1000) + 100,
				URL:     r.Href,
				Title:   r.Title,
				Summary: summary.Summary,
			}
			found = true
			result.SourceTitle = r.Title
			result.SourceURL = r.Href
			log.Printf("[Mode A] Found relevant source: %s", r.Title)
			actionLog.WriteString(fmt.Sprintf("- **Selected source:** [%s](%s)\n", r.Title, r.Href))
			break
		}
	}

	if !found {
		actionLog.WriteString("- **Result:** No suitable non-encyclopedia sources found\n")
		return fmt.Errorf("no suitable non-encyclopedia sources found for %s", topic)
	}

	// Generate a mini-article from the new source
	log.Printf("[Mode A] Generating mini-article from new source")
	newArticle, err := a.llm.GenerateMiniArticle(ctx, topic, sourceInfo.Title, sourceInfo.Summary)
	if err != nil {
		// Mark this source as failed so we don't retry it
		failedSources[sourceInfo.URL] = true
		log.Printf("[Mode A] Marking source as failed: %s", sourceInfo.URL)
		actionLog.WriteString(fmt.Sprintf("- **Error:** Failed to generate mini-article: %v\n", err))
		return fmt.Errorf("failed to generate mini-article: %w", err)
	}

	// Save the temporary article to debug
	a.saveDebugText(branchName, fmt.Sprintf("%s/temp-article-%s.md", debugBasePath(slug), time.Now().Format("20060102-150405")), "Save temp article", newArticle)

	// Extract sections from new article
	newSections, err := a.llm.ExtractSections(ctx, newArticle)
	if err != nil {
		actionLog.WriteString(fmt.Sprintf("- **Error:** Failed to extract sections from new article: %v\n", err))
		return fmt.Errorf("failed to extract sections: %w", err)
	}

	actionLog.WriteString(fmt.Sprintf("\n### Section Comparison\n\n"))
	actionLog.WriteString(fmt.Sprintf("- Existing article has %d sections\n", len(existingSections)))
	actionLog.WriteString(fmt.Sprintf("- New article has %d sections\n", len(newSections)))

	// Format sections for comparison
	existingSectionsStr := formatSectionsForLLM(existingSections)
	newSectionsStr := formatSectionsForLLM(newSections)

	// Compare sections
	comparison, err := a.llm.CompareSections(ctx, topic, articleContent, existingSectionsStr, newArticle, newSectionsStr)
	if err != nil {
		actionLog.WriteString(fmt.Sprintf("- **Error:** Section comparison failed: %v\n", err))
		return fmt.Errorf("section comparison failed: %w", err)
	}

	if !comparison.HasNewSection {
		actionLog.WriteString(fmt.Sprintf("- **Result:** No valuable new section to add\n"))
		actionLog.WriteString(fmt.Sprintf("- **Reason:** %s\n", comparison.Reason))
		log.Printf("[Mode A] No new section to add: %s", comparison.Reason)
		return nil
	}

	actionLog.WriteString(fmt.Sprintf("- **New section found:** %s\n", comparison.SectionTitle))
	actionLog.WriteString(fmt.Sprintf("- **Insert after:** %s\n", comparison.InsertAfter))
	actionLog.WriteString(fmt.Sprintf("- **Reason:** %s\n", comparison.Reason))

	// Insert the new section into the article
	updatedContent := insertSection(articleContent, comparison.InsertAfter, comparison.SectionTitle, comparison.SectionContent)

	// Update iteration count
	updatedContent = incrementIterationCount(updatedContent)

	// Save updated article
	if err := a.gh.UpdateFile(branchName, articlePath, fmt.Sprintf("Add section '%s' to %s", comparison.SectionTitle, topic), updatedContent, articleSHA); err != nil {
		actionLog.WriteString(fmt.Sprintf("- **Error:** Failed to save article: %v\n", err))
		return fmt.Errorf("failed to save article: %w", err)
	}

	// Save source
	authMgr := authority.NewManager(a.gh)
	_ = a.saveSourceSummary(ctx, sourceInfo, topic, slug, branchName, authMgr, false)

	result.SectionName = comparison.SectionTitle
	actionLog.WriteString(fmt.Sprintf("\n### Result\n\n- **Success:** Added section '%s' to article\n", comparison.SectionTitle))
	log.Printf("[Mode A] Successfully added section '%s' to article '%s'", comparison.SectionTitle, topic)
	return nil
}

// improveModeImproveSection implements Mode B: Select existing section, search for details, improve it
func (a *Agent) improveModeImproveSection(ctx context.Context, topic, slug, branchName, articlePath, articleContent, articleSHA string, existingSections []llm.ArticleSection, actionLog *strings.Builder, result *ImprovementResult, failedSources map[string]bool) error {
	log.Printf("[Mode B] Improving existing section for topic '%s'", topic)

	if len(existingSections) == 0 {
		actionLog.WriteString("- **Result:** No sections found to improve\n")
		return fmt.Errorf("no sections found to improve")
	}

	// Select a random section (prefer level 2 sections)
	var level2Sections []llm.ArticleSection
	for _, s := range existingSections {
		if s.Level == 2 && s.Title != "References" {
			level2Sections = append(level2Sections, s)
		}
	}

	var selectedSection llm.ArticleSection
	if len(level2Sections) > 0 {
		selectedSection = level2Sections[rand.Intn(len(level2Sections))]
	} else {
		// Filter out References section
		var nonRefSections []llm.ArticleSection
		for _, s := range existingSections {
			if s.Title != "References" {
				nonRefSections = append(nonRefSections, s)
			}
		}
		if len(nonRefSections) == 0 {
			actionLog.WriteString("- **Result:** No suitable sections to improve\n")
			return fmt.Errorf("no suitable sections to improve")
		}
		selectedSection = nonRefSections[rand.Intn(len(nonRefSections))]
	}

	actionLog.WriteString(fmt.Sprintf("\n### Selected Section: %s\n\n", selectedSection.Title))

	// Extract the current section content from the article
	currentSectionContent := extractSectionContent(articleContent, selectedSection.Title)
	if currentSectionContent == "" {
		actionLog.WriteString("- **Warning:** Could not extract section content\n")
	}

	// Search for more details about this specific section
	query := fmt.Sprintf("%s %s", topic, selectedSection.Title)
	log.Printf("[Mode B] Searching for: %s", query)
	actionLog.WriteString(fmt.Sprintf("- **Search query:** %s\n", query))

	results, err := a.search.Search(query)
	if err != nil {
		actionLog.WriteString(fmt.Sprintf("- **Error:** Search failed: %v\n", err))
		return fmt.Errorf("search failed: %w", err)
	}

	actionLog.WriteString(fmt.Sprintf("- Found %d search results\n", len(results)))

	var sourceInfo SourceInfo
	found := false

	result.SectionName = selectedSection.Title

	for _, r := range results {
		if strings.HasSuffix(r.Href, ".pdf") {
			continue
		}

		// Skip sources that have already failed in this run
		if failedSources[r.Href] {
			log.Printf("[Mode B] Skipping previously failed source: %s", r.Href)
			actionLog.WriteString(fmt.Sprintf("- Skipped previously failed: %s\n", r.Href))
			continue
		}

		// Check if source is an encyclopedia
		domain := extractDomain(r.Href)
		encCheck, err := a.llm.IsEncyclopediaSource(ctx, domain, r.Href, r.Title)
		if err != nil {
			log.Printf("[Mode B] Failed to check encyclopedia status for %s: %v", domain, err)
		} else if encCheck.IsEncyclopedia {
			log.Printf("[Mode B] Skipping encyclopedia source: %s", domain)
			actionLog.WriteString(fmt.Sprintf("- Skipped encyclopedia: %s\n", domain))
			result.SkippedSources = append(result.SkippedSources, domain)
			continue
		}

		log.Printf("[Mode B] Fetching content from: %s", r.Href)
		content, err := a.search.FetchContent(r.Href)
		if err != nil {
			continue
		}

		summary, err := a.llm.SummarizeSource(ctx, topic, r.Href, content)
		if err != nil {
			continue
		}

		if summary.Relevant {
			sourceInfo = SourceInfo{
				Index:   rand.Intn(1000) + 100,
				URL:     r.Href,
				Title:   r.Title,
				Summary: summary.Summary,
			}
			found = true
			result.SourceTitle = r.Title
			result.SourceURL = r.Href
			actionLog.WriteString(fmt.Sprintf("- **Selected source:** [%s](%s)\n", r.Title, r.Href))
			break
		}
	}

	if !found {
		actionLog.WriteString("- **Result:** No suitable non-encyclopedia sources found\n")
		return fmt.Errorf("no suitable sources found for section improvement")
	}

	// Merge the current section with new information
	log.Printf("[Mode B] Merging section content")
	mergedSection, err := a.llm.MergeSection(ctx, topic, selectedSection.Title, currentSectionContent, sourceInfo.Summary)
	if err != nil {
		actionLog.WriteString(fmt.Sprintf("- **Error:** Failed to merge section: %v\n", err))
		return fmt.Errorf("failed to merge section: %w", err)
	}

	// Score the improvement
	score, err := a.llm.ScoreImprovement(ctx, topic, selectedSection.Title, currentSectionContent, mergedSection)
	if err != nil {
		actionLog.WriteString(fmt.Sprintf("- **Warning:** Failed to score improvement: %v\n", err))
		// Continue anyway with a default score
		score = &llm.ImprovementScore{Score: 7, Recommendation: "accept", IsImproved: true}
	}

	result.Score = score.Score
	actionLog.WriteString(fmt.Sprintf("\n### Improvement Score\n\n"))
	actionLog.WriteString(fmt.Sprintf("- **Score:** %d/10\n", score.Score))
	actionLog.WriteString(fmt.Sprintf("- **Recommendation:** %s\n", score.Recommendation))
	if len(score.Improvements) > 0 {
		actionLog.WriteString(fmt.Sprintf("- **Improvements:** %s\n", strings.Join(score.Improvements, ", ")))
	}
	if len(score.Concerns) > 0 {
		actionLog.WriteString(fmt.Sprintf("- **Concerns:** %s\n", strings.Join(score.Concerns, ", ")))
	}

	if score.Score < 7 || score.Recommendation != "accept" {
		actionLog.WriteString(fmt.Sprintf("\n- **Result:** Improvement rejected (score too low)\n"))
		log.Printf("[Mode B] Improvement rejected: score=%d, recommendation=%s", score.Score, score.Recommendation)
		return fmt.Errorf("improvement rejected: score %d/10", score.Score)
	}

	// Replace the section in the article
	updatedContent := replaceSectionContent(articleContent, selectedSection.Title, mergedSection)

	// Update iteration count
	updatedContent = incrementIterationCount(updatedContent)

	// Save updated article
	if err := a.gh.UpdateFile(branchName, articlePath, fmt.Sprintf("Improve section '%s' in %s", selectedSection.Title, topic), updatedContent, articleSHA); err != nil {
		actionLog.WriteString(fmt.Sprintf("- **Error:** Failed to save article: %v\n", err))
		return fmt.Errorf("failed to save article: %w", err)
	}

	// Save source
	authMgr := authority.NewManager(a.gh)
	_ = a.saveSourceSummary(ctx, sourceInfo, topic, slug, branchName, authMgr, false)

	actionLog.WriteString(fmt.Sprintf("\n### Result\n\n- **Success:** Improved section '%s'\n", selectedSection.Title))
	log.Printf("[Mode B] Successfully improved section '%s' in article '%s'", selectedSection.Title, topic)
	return nil
}

// extractDomain extracts the domain from a URL
func extractDomain(urlStr string) string {
	u, err := url.Parse(urlStr)
	if err != nil {
		return urlStr
	}
	return u.Host
}

// formatSectionsForLLM formats section list for LLM input
func formatSectionsForLLM(sections []llm.ArticleSection) string {
	var sb strings.Builder
	for _, s := range sections {
		prefix := strings.Repeat("#", s.Level)
		sb.WriteString(fmt.Sprintf("%s %s\n", prefix, s.Title))
	}
	return sb.String()
}

// extractSectionContent extracts a section's content from the article
func extractSectionContent(article, sectionTitle string) string {
	lines := strings.Split(article, "\n")
	var content strings.Builder
	inSection := false
	sectionLevel := 0

	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line)

		// Check if this is a heading
		if strings.HasPrefix(trimmedLine, "#") {
			headingLevel := 0
			for _, c := range trimmedLine {
				if c == '#' {
					headingLevel++
				} else {
					break
				}
			}
			headingTitle := strings.TrimSpace(strings.TrimLeft(trimmedLine, "#"))

			if inSection {
				// If we hit a heading of same or higher level, we're done
				if headingLevel <= sectionLevel {
					break
				}
			}

			if headingTitle == sectionTitle {
				inSection = true
				sectionLevel = headingLevel
				content.WriteString(line + "\n")
				continue
			}
		}

		if inSection {
			content.WriteString(line + "\n")
		}
	}

	return strings.TrimSpace(content.String())
}

// insertSection inserts a new section after the specified section
func insertSection(article, insertAfter, newTitle, newContent string) string {
	lines := strings.Split(article, "\n")
	var result strings.Builder
	inserted := false

	for i, line := range lines {
		result.WriteString(line + "\n")

		if !inserted && strings.TrimSpace(line) != "" {
			trimmedLine := strings.TrimSpace(line)
			// Check if this is the section we want to insert after
			if strings.HasPrefix(trimmedLine, "#") {
				headingTitle := strings.TrimSpace(strings.TrimLeft(trimmedLine, "#"))
				if headingTitle == insertAfter {
					// Find the end of this section (next heading of same or higher level)
					headingLevel := 0
					for _, c := range trimmedLine {
						if c == '#' {
							headingLevel++
						} else {
							break
						}
					}

					// Continue until we find the next section of same/higher level
					for j := i + 1; j < len(lines); j++ {
						trimmedNextLine := strings.TrimSpace(lines[j])
						if strings.HasPrefix(trimmedNextLine, "#") {
							nextLevel := 0
							for _, c := range trimmedNextLine {
								if c == '#' {
									nextLevel++
								} else {
									break
								}
							}
							if nextLevel <= headingLevel {
								// Insert new section before this line
								// Write remaining lines up to here
								for k := i + 1; k < j; k++ {
									result.WriteString(lines[k] + "\n")
								}
								// Insert new section
								result.WriteString(fmt.Sprintf("\n%s %s\n\n%s\n\n", strings.Repeat("#", headingLevel), newTitle, newContent))
								inserted = true

								// Write remaining lines
								for k := j; k < len(lines); k++ {
									result.WriteString(lines[k] + "\n")
								}
								return strings.TrimRight(result.String(), "\n")
							}
						}
					}
				}
			}
		}
	}

	// If we couldn't find a good insertion point, append at the end (before References)
	if !inserted {
		lines := strings.Split(article, "\n")
		var result2 strings.Builder
		for i, line := range lines {
			trimmedLine := strings.TrimSpace(line)
			if strings.HasPrefix(trimmedLine, "## References") || strings.HasPrefix(trimmedLine, "# References") {
				result2.WriteString(fmt.Sprintf("\n## %s\n\n%s\n\n", newTitle, newContent))
				for j := i; j < len(lines); j++ {
					result2.WriteString(lines[j] + "\n")
				}
				return strings.TrimRight(result2.String(), "\n")
			}
			result2.WriteString(line + "\n")
		}
		// No references section, append at end
		result2.WriteString(fmt.Sprintf("\n## %s\n\n%s\n", newTitle, newContent))
		return strings.TrimRight(result2.String(), "\n")
	}

	return strings.TrimRight(result.String(), "\n")
}

// replaceSectionContent replaces a section's content in the article
func replaceSectionContent(article, sectionTitle, newContent string) string {
	lines := strings.Split(article, "\n")
	var result strings.Builder
	inSection := false
	sectionLevel := 0
	sectionReplaced := false

	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line)

		// Check if this is a heading
		if strings.HasPrefix(trimmedLine, "#") {
			headingLevel := 0
			for _, c := range trimmedLine {
				if c == '#' {
					headingLevel++
				} else {
					break
				}
			}
			headingTitle := strings.TrimSpace(strings.TrimLeft(trimmedLine, "#"))

			if inSection {
				// If we hit a heading of same or higher level, we're done with the section
				if headingLevel <= sectionLevel {
					inSection = false
					// Insert new content and continue
					if !sectionReplaced {
						result.WriteString(newContent + "\n\n")
						sectionReplaced = true
					}
				}
			}

			if headingTitle == sectionTitle && !sectionReplaced {
				inSection = true
				sectionLevel = headingLevel
				// Don't write the old heading, the new content includes it
				continue
			}
		}

		if inSection {
			// Skip old content
			continue
		}

		result.WriteString(line + "\n")
	}

	// If section was at the end and we haven't written new content yet
	if inSection && !sectionReplaced {
		result.WriteString(newContent + "\n")
	}

	return strings.TrimRight(result.String(), "\n")
}

// incrementIterationCount increments the iterations field in frontmatter
func incrementIterationCount(content string) string {
	lines := strings.Split(content, "\n")
	var result strings.Builder
	inFrontmatter := false
	foundIterations := false

	for _, line := range lines {
		if line == "---" {
			if !inFrontmatter {
				inFrontmatter = true
			} else {
				// End of frontmatter
				if !foundIterations {
					result.WriteString("iterations: 1\n")
				}
				inFrontmatter = false
			}
			result.WriteString(line + "\n")
			continue
		}

		if inFrontmatter && strings.HasPrefix(line, "iterations:") {
			// Parse and increment
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				valStr := strings.TrimSpace(parts[1])
				val, err := strconv.Atoi(valStr)
				if err == nil {
					result.WriteString(fmt.Sprintf("iterations: %d\n", val+1))
					foundIterations = true
					continue
				}
			}
		}

		result.WriteString(line + "\n")
	}

	return strings.TrimRight(result.String(), "\n")
}
