package agent

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"os"
	"path/filepath"
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

func NewAgentWithDeps(gh github.GitHubClient, s search.Searcher, l llm.Generator) *Agent {
	return &Agent{
		gh:     gh,
		search: s,
		llm:    l,
	}
}

func (a *Agent) Run(ctx context.Context) error {
	log.Println("Checking for research category issues...")
	issues, err := a.gh.GetResearchRequests()
	if err != nil {
		return fmt.Errorf("failed to get issues: %w", err)
	}

	if len(issues) == 0 {
		log.Println("No research category issues found.")
		return nil
	}

	// Pick one random issue
	rand.Seed(time.Now().UnixNano())
	issue := issues[rand.Intn(len(issues))]

	return a.expandCategory(ctx, issue)
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
	var createdArticles []string
	for _, topic := range candidates {
		log.Printf("Processing topic: %s", topic)
		if err := a.processTopic(ctx, topic, category, branchName, authMgr); err != nil {
			log.Printf("Error processing topic '%s': %v", topic, err)
			continue
		}
		createdArticles = append(createdArticles, topic)
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

	return nil
}

func (a *Agent) processTopic(ctx context.Context, topic, category, branchName string, authMgr *authority.Manager) error {
	// Research
	queries := []string{
		topic + " encyclopedia facts",
		topic + " history context",
		topic + " summary overview",
	}

	var results []search.Result
	seenURLs := make(map[string]bool)

	for _, q := range queries {
		res, err := a.search.Search(q)
		if err != nil {
			log.Printf("Search warning for '%s': %v", q, err)
			continue
		}
		log.Printf("Search '%s' returned %d results", q, len(res))
		for _, r := range res {
			if !seenURLs[r.Href] {
				results = append(results, r)
				seenURLs[r.Href] = true
			}
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Deep Research: Fetch content for top results
	contextData := "Sources:\n"
	var references []string

	limit := 5
	if len(results) < limit {
		limit = len(results)
	}

	processedCount := 0
	for _, r := range results {
		if processedCount >= limit {
			break
		}
		// Skip PDF or non-text if possible (FetchContent might fail or return garbage)
		if strings.HasSuffix(r.Href, ".pdf") {
			continue
		}

		content, err := a.search.FetchContent(r.Href)
		if err != nil {
			log.Printf("Failed to fetch %s: %v", r.Href, err)
			continue
		}
		if len(content) < 100 {
			continue // Skip thin content
		}

		processedCount++
		contextData += fmt.Sprintf("[%d] Title: %s\nURL: %s\nContent: %s\n\n", processedCount, r.Title, r.Href, content)
		references = append(references, fmt.Sprintf("[^%d]: [%s](%s)", processedCount, r.Title, r.Href))
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
	slug := strings.ToLower(strings.ReplaceAll(topic, " ", "-"))
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
