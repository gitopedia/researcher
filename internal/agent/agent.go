package agent

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

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

	return &Agent{
		gh:     ghClient,
		search: search.NewClient(),
		llm:    llm.NewClient(),
	}, nil
}

// NewAgentWithDeps allows injecting dependencies for testing
func NewAgentWithDeps(gh github.GitHubClient, s search.Searcher, l llm.Generator) *Agent {
	return &Agent{
		gh:     gh,
		search: s,
		llm:    l,
	}
}

func (a *Agent) Run(ctx context.Context) error {
	log.Println("Checking for research requests...")
	issues, err := a.gh.GetResearchRequests()
	if err != nil {
		return fmt.Errorf("failed to get issues: %w", err)
	}

	if len(issues) == 0 {
		log.Println("No research requests found.")
		return nil
	}

	for _, issue := range issues {
		if err := a.processIssue(ctx, issue); err != nil {
			log.Printf("Error processing issue #%d: %v", *issue.Number, err)
			continue
		}
	}
	return nil
}

func (a *Agent) processIssue(ctx context.Context, issue *gh.Issue) error {
	log.Printf("Processing Issue #%d: %s", *issue.Number, *issue.Title)

	topic := strings.TrimSpace(strings.Replace(*issue.Title, "Article Request:", "", 1))
	if topic == "" {
		return fmt.Errorf("could not parse topic from title")
	}

	// Research
	log.Printf("Researching '%s'...", topic)
	results, err := a.search.Search(topic + " encyclopedia facts")
	if err != nil {
		log.Printf("Search failed: %v", err)
		// Continue even if search fails (LLM might know enough)
	}

	contextData := ""
	for _, r := range results {
		contextData += fmt.Sprintf("Source: %s (%s)\nSummary: %s\n\n", r.Title, r.Href, r.Body)
	}

	// Draft Content
	log.Println("Drafting article...")
	content, err := a.llm.GenerateArticle(ctx, topic, contextData)
	if err != nil {
		return fmt.Errorf("LLM generation failed: %w", err)
	}

	// Add Front Matter
	id := ulid.Make()
	slug := strings.ToLower(strings.ReplaceAll(topic, " ", "-"))
	date := time.Now().Format("2006-01-02")

	var fullContent string
	if strings.HasPrefix(strings.TrimSpace(content), "---") {
		// Inject system fields into existing front matter
		lines := strings.Split(content, "\n")
		if len(lines) > 1 {
			systemFields := fmt.Sprintf("id: %s\nslug: \"%s\"\ncreated: %s\nauthor: \"Gitopedia Bot\"", id, slug, date)
			// Insert after the first "---" line
			newLines := append([]string{lines[0], systemFields}, lines[1:]...)
			fullContent = strings.Join(newLines, "\n")
		} else {
			fullContent = fmt.Sprintf("---\nid: %s\ntitle: \"%s\"\nslug: \"%s\"\ncreated: %s\nauthor: \"Gitopedia Bot\"\ntags: []\nsummary: \"\"\n---\n\n%s", id, topic, slug, date, content)
		}
	} else {
		// Fallback if LLM didn't generate front matter
		frontMatter := fmt.Sprintf(`---
id: %s
title: "%s"
slug: "%s"
created: %s
author: "Gitopedia Bot"
tags: []
summary: ""
---

`, id, topic, slug, date)
		fullContent = frontMatter + content
	}

	// Create PR
	branchName := fmt.Sprintf("research/issue-%d-%s", *issue.Number, slug)
	log.Printf("Creating branch %s...", branchName)

	// Assume 'main' is the base branch
	if err := a.gh.CreateBranch("main", branchName); err != nil {
		return fmt.Errorf("failed to create branch: %w", err)
	}

	filePath := fmt.Sprintf("Compendium/_incoming/%s.md", slug)
	if err := a.gh.CreateFile(branchName, filePath, fmt.Sprintf("Add article: %s", topic), fullContent); err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}

	prBody := fmt.Sprintf("Auto-generated article for '%s'.\n\nCloses #%d", topic, *issue.Number)
	pr, err := a.gh.CreatePullRequest(fmt.Sprintf("Add article: %s", topic), prBody, branchName, "main")
	if err != nil {
		return fmt.Errorf("failed to create PR: %w", err)
	}

	log.Printf("Created PR #%d", *pr.Number)

	// Add comment
	if err := a.gh.CommentOnIssue(*issue.Number, fmt.Sprintf("Researcher has opened a Draft PR: %s", *pr.HTMLURL)); err != nil {
		log.Printf("Failed to comment on issue: %v", err)
	}

	return nil
}
