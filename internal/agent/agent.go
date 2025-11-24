package agent

import (
	"context"
	"fmt"
	"log"
	"strings"
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

	// 1. Load Authorities
	authMgr := authority.NewManager(a.gh)
	if err := authMgr.Load("main"); err != nil {
		log.Printf("Warning: failed to load authorities: %v", err)
	}

	// 2. Research
	log.Printf("Researching '%s'...", topic)
	queries := []string{
		topic + " encyclopedia facts",
		topic + " history context",
		topic + " summary overview",
	}

	var results []search.Result
	seenURLs := make(map[string]bool)

	for _, q := range queries {
		log.Printf("Searching for: %s", q)
		res, err := a.search.Search(q)
		if err != nil {
			log.Printf("Search failed for '%s': %v", q, err)
			continue
		}
		for _, r := range res {
			if !seenURLs[r.Href] {
				results = append(results, r)
				seenURLs[r.Href] = true
			}
		}
		time.Sleep(1 * time.Second) // polite delay between queries
	}

	if len(results) == 0 {
		log.Printf("Warning: No search results found for topic '%s'", topic)
	}

	contextData := ""
	for i, r := range results {
		contextData += fmt.Sprintf("[%d] Title: %s\nURL: %s\nSummary: %s\n\n", i+1, r.Title, r.Href, r.Body)
	}

	// 3. Draft Content
	log.Println("Drafting article...")
	content, err := a.llm.GenerateArticle(ctx, topic, contextData)
	if err != nil {
		return fmt.Errorf("LLM generation failed: %w", err)
	}

	// 4. Extract Entities and Resolve Authorities
	log.Println("Extracting entities...")
	extracted, err := a.llm.ExtractEntities(ctx, content)
	if err != nil {
		log.Printf("Warning: entity extraction failed: %v", err)
	}

	// Always include the main topic as a Topic entity if not extracted
	foundTopic := false
	for _, e := range extracted {
		if strings.EqualFold(e.Name, topic) && e.Type == llm.Topic {
			foundTopic = true
			break
		}
	}
	if !foundTopic {
		extracted = append(extracted, llm.ExtractedEntity{Name: topic, Type: llm.Topic})
	}

	resolved, err := authMgr.ResolveEntities(extracted)
	if err != nil {
		log.Printf("Warning: entity resolution failed: %v", err)
	}

	// 5. Add/Update Front Matter with Facets
	id := ulid.Make()
	slug := strings.ToLower(strings.ReplaceAll(topic, " ", "-"))
	date := time.Now().Format("2006-01-02")

	// Construct tags from resolved topics
	var tags []string
	if topicIDs, ok := resolved["topic"]; ok {
		tags = topicIDs
	} else {
		// Fallback to slug if no authority resolution
		tags = []string{"topic:" + slug}
	}
	tagsStr := fmt.Sprintf("[\"%s\"]", strings.Join(tags, "\", \""))

	// Construct other facets strings
	facetsBlock := ""
	if peopleIDs, ok := resolved["person"]; ok && len(peopleIDs) > 0 {
		facetsBlock += fmt.Sprintf("people: [\"%s\"]\n", strings.Join(peopleIDs, "\", \""))
	}
	if orgIDs, ok := resolved["org"]; ok && len(orgIDs) > 0 {
		facetsBlock += fmt.Sprintf("orgs: [\"%s\"]\n", strings.Join(orgIDs, "\", \""))
	}
	if placeIDs, ok := resolved["place"]; ok && len(placeIDs) > 0 {
		facetsBlock += fmt.Sprintf("places: [\"%s\"]\n", strings.Join(placeIDs, "\", \""))
	}

	var fullContent string
	if strings.HasPrefix(strings.TrimSpace(content), "---") {
		// Inject fields into existing front matter
		lines := strings.Split(content, "\n")
		if len(lines) > 1 {
			systemFields := fmt.Sprintf("id: %s\nslug: \"%s\"\ncreated: %s\nauthor: \"Gitopedia Bot\"", id, slug, date)
			// Replace tags line if it exists, otherwise append
			// For MVP, simple injection. Ideally use YAML parser.
			// We'll just append our facets block and let YAML parser handle duplicate keys (last one wins usually) or just append.
			// But simpler: Inject our tags and facets.
			// Note: LLM generated `tags: [...]` might conflict.
			// We'll try to replace `tags: .*` with our tags.
			// Actually, let's trust our resolved tags more? Or merge?
			// LLM tags are strings. Our resolved tags are IDs.
			// Let's just append our block after system fields.
			
			// Overwriting tags logic:
			// We'll just append `tags: [...]` which in YAML usually overrides previous key if not careful, or valid YAML allows duplicates but parser behavior varies.
			// Safe bet: Remove LLM tags line if possible.
			// Simple string manipulation:
			
			var cleanedLines []string
			for _, line := range lines {
				if strings.HasPrefix(strings.TrimSpace(line), "tags:") {
					continue // Remove LLM tags
				}
				cleanedLines = append(cleanedLines, line)
			}
			
			// Insert after first "---"
			injection := fmt.Sprintf("%s\ntags: %s\n%s", systemFields, tagsStr, facetsBlock)
			newLines := append([]string{cleanedLines[0], injection}, cleanedLines[1:]...)
			fullContent = strings.Join(newLines, "\n")
		} else {
			// Malformed
			fullContent = content
		}
	} else {
		frontMatter := fmt.Sprintf(`---
id: %s
title: "%s"
slug: "%s"
created: %s
author: "Gitopedia Bot"
tags: %s
%ssummary: ""
---

`, id, topic, slug, date, tagsStr, facetsBlock)
		fullContent = frontMatter + content
	}

	// 6. Create Branch
	branchName := fmt.Sprintf("research/issue-%d-%s", *issue.Number, slug)
	log.Printf("Creating branch %s...", branchName)

	if err := a.gh.CreateBranch("main", branchName); err != nil {
		return fmt.Errorf("failed to create branch: %w", err)
	}

	// 7. Commit Authority Updates
	updates, err := authMgr.GetUpdates()
	if err != nil {
		log.Printf("Failed to get authority updates: %v", err)
	} else {
		for path, update := range updates {
			log.Printf("Updating authority file: %s", path)
			if err := a.gh.UpdateFile(branchName, path, "Update authority " + path, update.Content, update.SHA); err != nil {
				// Try CreateFile if Update fails (e.g. if we thought it existed but it didn't)?
				// No, Manager.Load handled existence.
				// If SHA is empty (new file), UpdateFile might fail if we didn't handle it?
				// My `UpdateFile` takes SHA. If SHA is empty, GitHub API might complain for Update.
				// If SHA is empty, use CreateFile.
				if update.SHA == "" {
					if err := a.gh.CreateFile(branchName, path, "Create authority " + path, update.Content); err != nil {
						log.Printf("Failed to create authority file %s: %v", path, err)
					}
				} else {
					if err := a.gh.UpdateFile(branchName, path, "Update authority " + path, update.Content, update.SHA); err != nil {
						log.Printf("Failed to update authority file %s: %v", path, err)
					}
				}
			}
		}
	}

	// 8. Commit Article
	filePath := fmt.Sprintf("Compendium/_incoming/%s.md", slug)
	if err := a.gh.CreateFile(branchName, filePath, fmt.Sprintf("Add article: %s", topic), fullContent); err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}

	// 9. Create PR
	prBody := fmt.Sprintf("Auto-generated article for '%s'.\n\nCloses #%d", topic, *issue.Number)
	pr, err := a.gh.CreatePullRequest(fmt.Sprintf("Add article: %s", topic), prBody, branchName, "main")
	if err != nil {
		return fmt.Errorf("failed to create PR: %w", err)
	}

	log.Printf("Created PR #%d", *pr.Number)

	if err := a.gh.CommentOnIssue(*issue.Number, fmt.Sprintf("Researcher has opened a Draft PR: %s", *pr.HTMLURL)); err != nil {
		log.Printf("Failed to comment on issue: %v", err)
	}

	return nil
}
