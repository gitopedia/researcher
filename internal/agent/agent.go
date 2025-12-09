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

var Version = "dev"

func init() {
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

func debugBasePath(slug string) string {
	return fmt.Sprintf("Compendium/_debug/articles/%s", slug)
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

func (a *Agent) MergeOnly(ctx context.Context) error {
	log.Println("Running merge-only mode...")
	return a.mergeReadyPRs(ctx)
}

func (a *Agent) Run(ctx context.Context) error {
	if err := a.mergeReadyPRs(ctx); err != nil {
		slog.Warn("Error checking/merging PRs", "error", err)
	}

	log.Println("Checking for research category issues...")
	issues, err := a.gh.GetResearchRequests()
	if err != nil {
		return fmt.Errorf("failed to get issues: %w", err)
	}

	log.Printf("Found %d research category issues", len(issues))

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

	var availableIssues []*gh.Issue
	for _, issue := range issues {
		if !issuesWithPRs[*issue.Number] {
			availableIssues = append(availableIssues, issue)
		}
	}

	maxConcurrent := 1
	if v := os.Getenv("MAX_CONCURRENT_PRS"); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			maxConcurrent = i
		}
	}

	log.Printf("Status: Managed PRs: %d (Limit: %d), Available Issues: %d", len(managedPRs), maxConcurrent, len(availableIssues))

	if len(managedPRs) < maxConcurrent && len(availableIssues) > 0 {
		rand.Seed(time.Now().UnixNano())
		issue := availableIssues[rand.Intn(len(availableIssues))]
		return a.processNewTopic(ctx, issue)
	}

	if len(managedPRs) > 0 {
		rand.Seed(time.Now().UnixNano())
		pr := managedPRs[rand.Intn(len(managedPRs))]
		return a.processExistingPR(ctx, pr)
	}

	log.Println("No work to do (no available issues for new topics, no managed PRs to update)")
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
