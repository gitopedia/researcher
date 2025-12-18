package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/gitopedia/researcher/internal/authority"
	"github.com/gitopedia/researcher/internal/llm"
	"github.com/gitopedia/researcher/internal/search"
	gh "github.com/google/go-github/v57/github"
	"github.com/oklog/ulid/v2"
)

const (
	LabelStatusDiscovery  = "research:status-discovery"
	LabelStatusSummarized = "research:status-summarized"
	LabelStatusDrafted    = "research:status-drafted"
	LabelManualReview     = "research:manual-review"

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
	issueNum := *issue.Number
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

	// Check if manual review is active and we are not forcing a manual step
	if manualStep == "" {
		hasReview, err := a.gh.HasLabel(issueNum, LabelManualReview)
		if err == nil && hasReview {
			log.Printf("Issue #%d is waiting for manual review. Skipping.", issueNum)
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

	comment := fmt.Sprintf("## Research Discovery: %s\n\nI found %d potential sources. I have saved them to the debug folder for your review:\n`%s` in branch `%s`.\n\nRemove the `%s` label to proceed with the first relevant source.", topic, len(results), stepDir, state.Branch, LabelManualReview)
	if !a.gh.IsLocal() {
		if err := a.gh.CommentOnIssue(*issue.Number, comment); err != nil {
			return err
		}
		if err := a.gh.AddLabel(*issue.Number, LabelStatusDiscovery); err != nil {
			return err
		}
		return a.gh.AddLabel(*issue.Number, LabelManualReview)
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
		return fmt.Errorf("no relevant sources found during summarization step")
	}

	authMgr := authority.NewManager(a.gh)
	_ = authMgr.Load("main")

	if err := a.saveSourceSummary(ctx, sourceInfo, topic, slug, branchName, authMgr, false); err != nil {
		return err
	}

	// Save summary to _debug folder
	stepDir := fmt.Sprintf("%s/step-2-summarization", debugBasePath(slug))
	a.saveDebugText(branchName, fmt.Sprintf("%s/summary-1.md", stepDir), "Save source summary", sourceInfo.Summary)

	comment := fmt.Sprintf("## Research Summarization\n\nI have summarized the source: [%s](%s).\n\nThe full summary is available in the debug folder:\n`%s` in branch `%s`.\n\nRemove `%s` to proceed to article generation.", sourceInfo.Title, sourceInfo.URL, stepDir, branchName, LabelManualReview)
	if !a.gh.IsLocal() {
		if err := a.gh.CommentOnIssue(*issue.Number, comment); err != nil {
			return err
		}
		if err := a.gh.AddLabel(*issue.Number, LabelStatusSummarized); err != nil {
			return err
		}
		return a.gh.AddLabel(*issue.Number, LabelManualReview)
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
	frontMatter := fmt.Sprintf("---\nid: %s\ntitle: \"%s\"\nslug: \"%s\"\ncreated: %s\nresearcher_version: \"1\"\n---\n\n", id, topic, slug, date)
	fullContent := frontMatter + miniArticle

	articlePath := fmt.Sprintf("Compendium/_incoming/%s.md", slug)
	if err := a.gh.CreateFile(branchName, articlePath, "Draft article", fullContent); err != nil {
		return err
	}

	// Save drafting results
	stepDir := fmt.Sprintf("%s/step-3-drafting", debugBasePath(slug))
	a.saveDebugText(branchName, fmt.Sprintf("%s/article-draft.md", stepDir), "Save drafted article", fullContent)

	comment := fmt.Sprintf("## Research Drafting\n\nI have drafted the article. You can see it in branch `%s`. \n\nRemove `%s` to create the Pull Request.", branchName, LabelManualReview)
	if !a.gh.IsLocal() {
		_ = a.gh.CommentOnIssue(*issue.Number, comment)
		_ = a.gh.AddLabel(*issue.Number, LabelStatusDrafted)
		_ = a.gh.AddLabel(*issue.Number, LabelManualReview)
	}
	log.Println(comment)
	return nil
}

func (a *Agent) stepFinalize(ctx context.Context, issue *gh.Issue, state *ResearchState) error {
	topic := state.Topic
	branchName := state.Branch
	log.Printf("[Step: Finalize] Creating PR for '%s'...", topic)

	if a.gh.IsLocal() {
		log.Printf("In local mode, cannot create GitHub PR. Please push branch '%s' manually.", branchName)
		return nil
	}

	prTitle := fmt.Sprintf("Research: %s", topic)
	prBody := fmt.Sprintf("Initiated research on **%s**.\n\nCloses #%d", topic, *issue.Number)
	pr, err := a.gh.CreatePullRequest(prTitle, prBody, branchName, "main")
	if err != nil {
		return err
	}

	_ = a.gh.AddLabel(*issue.Number, "research:status-pr-created")
	_ = a.gh.RemoveLabel(*issue.Number, LabelStatusDiscovery)
	_ = a.gh.RemoveLabel(*issue.Number, LabelStatusSummarized)
	_ = a.gh.RemoveLabel(*issue.Number, LabelStatusDrafted)
	_ = a.gh.RemoveLabel(*issue.Number, LabelManualReview)

	return a.gh.CommentOnIssue(*issue.Number, fmt.Sprintf("Successfully created PR #%d", pr.Number))
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
		return fmt.Errorf("could not find a relevant source for %s", topic)
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
summary: "Initial overview based on %s"
---

`, id, topic, slug, date, tagsStr, facetsBlock, os.Getenv("LLM_MODEL_ARTICLE"), sourceInfo.Title)

	fullContent := frontMatter + miniArticle + fmt.Sprintf("\n\n## References\n\n[^1]: [%s](%s)", sourceInfo.Title, sourceInfo.URL)

	articlePath := fmt.Sprintf("Compendium/_incoming/%s.md", slug)
	if err := a.gh.CreateFile(branchName, articlePath, fmt.Sprintf("Init article: %s", topic), fullContent); err != nil {
		return fmt.Errorf("failed to create article file: %w", err)
	}

	// 6. Create PR
	if a.gh.IsLocal() {
		log.Printf("In local mode, cannot create GitHub PR. Please push branch '%s' manually.", branchName)
		return nil
	}

	prTitle := fmt.Sprintf("Research: %s", topic)
	prBody := fmt.Sprintf("Initiated research on **%s**.\n\nSource used: [%s](%s)\n\nCloses #%d", topic, sourceInfo.Title, sourceInfo.URL, *issue.Number)
	pr, err := a.gh.CreatePullRequest(prTitle, prBody, branchName, "main")
	if err != nil {
		return fmt.Errorf("failed to create PR: %w", err)
	}

	log.Printf("Created PR #%d", *pr.Number)
	a.gh.CommentOnIssue(*issue.Number, fmt.Sprintf("Started research in PR #%d", *pr.Number))

	return nil
}
