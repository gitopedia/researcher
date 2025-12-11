package agent

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/gitopedia/researcher/internal/authority"
	"github.com/gitopedia/researcher/internal/llm"
	gh "github.com/google/go-github/v57/github"
	"github.com/oklog/ulid/v2"
)

// processNewTopic handles the creation of a new research topic from scratch.
// It finds one source, creates a draft article, and opens a PR.
func (a *Agent) processNewTopic(ctx context.Context, issue *gh.Issue) error {
	title := *issue.Title
	// Cleanup title to get topic
	topic := strings.TrimSpace(strings.TrimPrefix(title, "Category:"))
	if topic == title {
		topic = strings.TrimSpace(strings.TrimPrefix(title, "Research:"))
	}
	if topic == title {
		// Fallback: just use the title
		topic = title
	}

	log.Printf("Starting NEW TOPIC flow for Issue #%d: '%s'", *issue.Number, topic)

	slug := strings.ToLower(strings.ReplaceAll(topic, " ", "-"))
	branchName := fmt.Sprintf("research/%s-%s", slug, time.Now().Format("20060102-150405"))

	// 1. Create Branch
	log.Printf("Creating branch %s...", branchName)
	if err := a.gh.CreateBranch("main", branchName); err != nil {
		return fmt.Errorf("failed to create branch: %w", err)
	}

	// 2. Search for 1 Source
	// We want *one* high quality source.
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
			log.Printf("Failed to fetch content: %v", err)
			continue
		}

		summary, err := a.llm.SummarizeSource(ctx, topic, r.Href, content)
		if err != nil {
			log.Printf("Failed to summarize: %v", err)
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
		} else {
			log.Printf("Source irrelevant: %s", summary.Reason)
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
	// Create frontmatter with version 1
	id := ulid.Make()
	date := time.Now().UTC().Format("2006-01-02T15:04:05Z")

	// Extract entities from the generated mini-article for tagging
	extracted, err := a.llm.ExtractEntities(ctx, miniArticle)
	if err != nil {
		slog.Warn("Entity extraction failed", "error", err)
	}
	// Ensure topic is in entities
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
	prTitle := fmt.Sprintf("Research: %s", topic)
	prBody := fmt.Sprintf("Initiated research on **%s**.\n\nSource used: [%s](%s)\n\nCloses #%d", topic, sourceInfo.Title, sourceInfo.URL, *issue.Number)
	pr, err := a.gh.CreatePullRequest(prTitle, prBody, branchName, "main")
	if err != nil {
		return fmt.Errorf("failed to create PR: %w", err)
	}

	log.Printf("Created PR #%d", *pr.Number)

	// Comment on issue
	a.gh.CommentOnIssue(*issue.Number, fmt.Sprintf("Started research in PR #%d", *pr.Number))

	return nil
}
