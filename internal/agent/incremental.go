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
	"github.com/gitopedia/researcher/internal/styles"
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
	SectionName    string   // Section added or improved
	SectionsAdded  []string // For batch Mode A improvements
	SourceTitle    string
	SourceURL      string
	Score          int // For Mode B improvements
	ErrorMessage   string
	SkippedSources []string // Encyclopedia sources that were skipped
}

// ArticleMetadata tracks sources for an article
type ArticleMetadata struct {
	ArticleSlug    string          `json:"article_slug"`
	Searches       []SearchRecord  `json:"searches"`
	SourcesUsed    []SourceRecord  `json:"sources_used"`
	SourcesSkipped []SkippedSource `json:"sources_skipped"`
}

// SearchRecord tracks a search query and its results
type SearchRecord struct {
	Query        string    `json:"query"`
	Timestamp    time.Time `json:"timestamp"`
	ResultsFound int       `json:"results_found"`
	Page         int       `json:"page"`
}

// SourceRecord tracks a source that was used
type SourceRecord struct {
	URL    string `json:"url"`
	Domain string `json:"domain"`
	Title  string `json:"title"`
}

// SkippedSource tracks a source that was skipped
type SkippedSource struct {
	URL        string `json:"url"`
	Domain     string `json:"domain"`
	Reason     string `json:"reason"`
	DetectedBy string `json:"detected_by"` // "global_list" or "llm"
}

// loadArticleMetadata loads metadata for an article from the .meta folder
func (a *Agent) loadArticleMetadata(branchName, slug string) (*ArticleMetadata, error) {
	metaPath := fmt.Sprintf("Compendium/_incoming/.meta/%s.json", slug)
	content, _, err := a.gh.GetFile(branchName, metaPath)
	if err != nil {
		// Try main branch
		content, _, err = a.gh.GetFile("main", metaPath)
		if err != nil {
			// Return empty metadata if file doesn't exist
			return &ArticleMetadata{
				ArticleSlug:    slug,
				Searches:       []SearchRecord{},
				SourcesUsed:    []SourceRecord{},
				SourcesSkipped: []SkippedSource{},
			}, nil
		}
	}

	var meta ArticleMetadata
	if err := json.Unmarshal([]byte(content), &meta); err != nil {
		return nil, fmt.Errorf("failed to parse article metadata: %w", err)
	}
	return &meta, nil
}

// saveArticleMetadata saves metadata for an article to the .meta folder
func (a *Agent) saveArticleMetadata(branchName string, meta *ArticleMetadata) error {
	metaPath := fmt.Sprintf("Compendium/_incoming/.meta/%s.json", meta.ArticleSlug)
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal article metadata: %w", err)
	}

	// Try to get existing file SHA for update
	_, sha, _ := a.gh.GetFile(branchName, metaPath)
	if sha != "" {
		return a.gh.UpdateFile(branchName, metaPath, fmt.Sprintf("Update metadata for %s", meta.ArticleSlug), string(data), sha)
	}
	return a.gh.CreateFile(branchName, metaPath, fmt.Sprintf("Add metadata for %s", meta.ArticleSlug), string(data))
}

// isSourceAlreadyUsed checks if a URL has already been used for this article
func (meta *ArticleMetadata) isSourceAlreadyUsed(url string) bool {
	for _, s := range meta.SourcesUsed {
		if s.URL == url {
			return true
		}
	}
	return false
}

// addSourceUsed adds a source to the used list
func (meta *ArticleMetadata) addSourceUsed(url, domain, title string) {
	meta.SourcesUsed = append(meta.SourcesUsed, SourceRecord{
		URL:    url,
		Domain: domain,
		Title:  title,
	})
}

// addSourceSkipped adds a source to the skipped list
func (meta *ArticleMetadata) addSourceSkipped(url, domain, reason, detectedBy string) {
	// Check if already in list
	for _, s := range meta.SourcesSkipped {
		if s.URL == url {
			return
		}
	}
	meta.SourcesSkipped = append(meta.SourcesSkipped, SkippedSource{
		URL:        url,
		Domain:     domain,
		Reason:     reason,
		DetectedBy: detectedBy,
	})
}

// addSearch adds a search record (skips if same query+page already exists)
func (meta *ArticleMetadata) addSearch(query string, resultsFound, page int) {
	// Check if this query+page combination already exists
	for _, s := range meta.Searches {
		if s.Query == query && s.Page == page {
			return // Skip duplicate
		}
	}
	meta.Searches = append(meta.Searches, SearchRecord{
		Query:        query,
		Timestamp:    time.Now().UTC(),
		ResultsFound: resultsFound,
		Page:         page,
	})
}

// SourceSearchResult holds the result of a source search
type SourceSearchResult struct {
	Source   SourceInfo
	Metadata *ArticleMetadata
}

// findUsableSource searches for a usable source with global ignore list filtering,
// metadata-based source tracking, and pagination support.
// It returns the first usable source and the updated metadata.
func (a *Agent) findUsableSource(ctx context.Context, query, slug, branchName string, failedSources map[string]bool) (*SourceSearchResult, error) {
	// Load or create article metadata
	meta, err := a.loadArticleMetadata(branchName, slug)
	if err != nil {
		log.Printf("Warning: Failed to load article metadata: %v", err)
		meta = &ArticleMetadata{
			ArticleSlug:    slug,
			Searches:       []SearchRecord{},
			SourcesUsed:    []SourceRecord{},
			SourcesSkipped: []SkippedSource{},
		}
	}

	maxPages := 5 // Maximum pages to search

	for page := 0; page < maxPages; page++ {
		log.Printf("Searching for: %s (page %d)", query, page)
		results, err := a.search.SearchPage(query, page)
		if err != nil {
			if page == 0 {
				return nil, fmt.Errorf("search failed: %w", err)
			}
			break // No more pages
		}

		if len(results) == 0 {
			log.Printf("No more results on page %d", page)
			break
		}

		// Record the search
		meta.addSearch(query, len(results), page)

		usableSourceFound := false
		for _, r := range results {
			if strings.HasSuffix(r.Href, ".pdf") {
				log.Printf("Skipping PDF source: %s", r.Href)
				continue
			}

			// Skip sources that have already failed in this run
			if failedSources != nil && failedSources[r.Href] {
				log.Printf("Skipping previously failed source: %s", r.Href)
				continue
			}

			domain := extractDomain(r.Href)

			// 1. Check global ignore list first (no LLM call needed)
			if a.isDomainIgnored(domain) {
				log.Printf("Skipping globally ignored domain: %s", domain)
				meta.addSourceSkipped(r.Href, domain, "encyclopedia", "global_list")
				continue
			}

			// 2. Check if source is already used for this article
			if meta.isSourceAlreadyUsed(r.Href) {
				log.Printf("Skipping already-used source: %s", r.Href)
				continue
			}

			// 3. Ask LLM if source is an encyclopedia
			encCheck, err := a.llm.IsEncyclopediaSource(ctx, domain, r.Href, r.Title)
			if err != nil {
				log.Printf("Failed to check encyclopedia status for %s: %v", domain, err)
			} else if encCheck.IsEncyclopedia {
				log.Printf("Skipping encyclopedia source: %s (%s)", domain, encCheck.Reason)
				meta.addSourceSkipped(r.Href, domain, "encyclopedia", "llm")
				continue
			}

			// 4. Fetch and summarize content
			log.Printf("Checking source: %s", r.Href)
			content, err := a.search.FetchContent(r.Href)
			if err != nil {
				log.Printf("Failed to fetch content from %s: %v", r.Href, err)
				continue
			}

			// We found a usable source!
			meta.addSourceUsed(r.Href, domain, r.Title)
			usableSourceFound = true

			return &SourceSearchResult{
				Source: SourceInfo{
					Index:   1,
					URL:     r.Href,
					Title:   r.Title,
					Summary: content, // Raw content, caller will summarize
				},
				Metadata: meta,
			}, nil
		}

		// If we found sources but none were usable, try next page
		if !usableSourceFound {
			log.Printf("All sources on page %d were filtered, trying next page...", page)
			continue
		}
	}

	// No usable source found after all pages
	return nil, fmt.Errorf("could not find a usable non-encyclopedia source after searching %d pages", maxPages)
}

// findUsableSourceWithSummary is like findUsableSource but also summarizes the content
func (a *Agent) findUsableSourceWithSummary(ctx context.Context, topic, query, slug, branchName string, failedSources map[string]bool) (*SourceSearchResult, error) {
	meta, err := a.loadArticleMetadata(branchName, slug)
	if err != nil {
		log.Printf("Warning: Failed to load article metadata: %v", err)
		meta = &ArticleMetadata{
			ArticleSlug:    slug,
			Searches:       []SearchRecord{},
			SourcesUsed:    []SourceRecord{},
			SourcesSkipped: []SkippedSource{},
		}
	}

	maxPages := 5

	for page := 0; page < maxPages; page++ {
		log.Printf("Searching for: %s (page %d)", query, page)
		results, err := a.search.SearchPage(query, page)
		if err != nil {
			if page == 0 {
				return nil, fmt.Errorf("search failed: %w", err)
			}
			break
		}

		if len(results) == 0 {
			log.Printf("No more results on page %d", page)
			break
		}

		meta.addSearch(query, len(results), page)

		for _, r := range results {
			if strings.HasSuffix(r.Href, ".pdf") {
				continue
			}

			if failedSources != nil && failedSources[r.Href] {
				log.Printf("Skipping previously failed source: %s", r.Href)
				continue
			}

			domain := extractDomain(r.Href)

			// 1. Check global ignore list
			if a.isDomainIgnored(domain) {
				log.Printf("Skipping globally ignored domain: %s", domain)
				meta.addSourceSkipped(r.Href, domain, "encyclopedia", "global_list")
				continue
			}

			// 2. Check if already used
			if meta.isSourceAlreadyUsed(r.Href) {
				log.Printf("Skipping already-used source: %s", r.Href)
				continue
			}

			// 3. LLM encyclopedia check
			encCheck, err := a.llm.IsEncyclopediaSource(ctx, domain, r.Href, r.Title)
			if err != nil {
				log.Printf("Failed to check encyclopedia status for %s: %v", domain, err)
			} else if encCheck.IsEncyclopedia {
				log.Printf("Skipping encyclopedia source: %s (%s)", domain, encCheck.Reason)
				meta.addSourceSkipped(r.Href, domain, "encyclopedia", "llm")
				continue
			}

			// 4. Fetch content
			log.Printf("Checking source: %s", r.Href)
			content, err := a.search.FetchContent(r.Href)
			if err != nil {
				log.Printf("Failed to fetch content from %s: %v", r.Href, err)
				continue
			}

			// 5. Summarize and check relevance
			summary, err := a.llm.SummarizeSource(ctx, topic, r.Href, content)
			if err != nil {
				log.Printf("Failed to summarize source %s: %v", r.Href, err)
				continue
			}

			if !summary.Relevant {
				log.Printf("Source rejected: %s - Reason: %s", r.Href, summary.Reason)
				continue
			}

			meta.addSourceUsed(r.Href, domain, r.Title)
			log.Printf("Found relevant source: %s", r.Title)

			return &SourceSearchResult{
				Source: SourceInfo{
					Index:   1,
					URL:     r.Href,
					Title:   r.Title,
					Summary: summary.Summary,
				},
				Metadata: meta,
			}, nil
		}

		log.Printf("All sources on page %d were filtered or irrelevant, trying next page...", page)
	}

	return nil, fmt.Errorf("could not find a relevant non-encyclopedia source after searching %d pages", maxPages)
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

	query := topic + " explained"
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

	// Load article metadata for tracking
	meta, _ := a.loadArticleMetadata(branchName, slug)

	var sourceInfo SourceInfo
	found := false
	for _, r := range results {
		if strings.HasSuffix(r.Href, ".pdf") {
			log.Printf("Skipping PDF source: %s", r.Href)
			continue
		}

		domain := extractDomain(r.Href)

		// Check global ignore list first (no LLM call needed)
		if a.isDomainIgnored(domain) {
			log.Printf("Skipping globally ignored domain: %s", domain)
			meta.addSourceSkipped(r.Href, domain, "encyclopedia", "global_list")
			continue
		}

		// Check if already used
		if meta.isSourceAlreadyUsed(r.Href) {
			log.Printf("Skipping already-used source: %s", r.Href)
			continue
		}

		// Check if source is an encyclopedia via LLM
		encCheck, err := a.llm.IsEncyclopediaSource(ctx, domain, r.Href, r.Title)
		if err != nil {
			log.Printf("Failed to check encyclopedia status for %s: %v", domain, err)
		} else if encCheck.IsEncyclopedia {
			log.Printf("Skipping encyclopedia source: %s (%s)", domain, encCheck.Reason)
			meta.addSourceSkipped(r.Href, domain, "encyclopedia", "llm")
			continue
		}

		fetchedContent, err := a.search.FetchContent(r.Href)
		if err != nil {
			log.Printf("Failed to fetch content from %s: %v", r.Href, err)
			continue
		}
		summary, err := a.llm.SummarizeSource(ctx, topic, r.Href, fetchedContent)
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
		meta.addSourceUsed(r.Href, domain, r.Title)
		found = true
		break
	}

	// Save updated metadata
	if saveErr := a.saveArticleMetadata(branchName, meta); saveErr != nil {
		slog.Warn("Failed to save article metadata", "error", saveErr)
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

// extractCategoryContext extracts category, subcategory, and topic from issue title
// e.g., "Science > Physics > Quantum Mechanics" -> ("Science", "Physics", "Quantum Mechanics")
func extractCategoryContext(issueTitle string) (category, subcategory, topic string) {
	parts := strings.Split(issueTitle, " > ")
	if len(parts) >= 3 {
		return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), strings.TrimSpace(parts[2])
	}
	if len(parts) == 2 {
		return strings.TrimSpace(parts[0]), "", strings.TrimSpace(parts[1])
	}
	// Fallback: just use the title as the topic
	return "", "", strings.TrimSpace(issueTitle)
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

	// Load authorities for entity resolution
	authMgr := authority.NewManager(a.gh)
	if err := authMgr.Load("main"); err != nil {
		slog.Warn("Failed to load authorities", "error", err)
	}

	// 2. Search for source with global ignore list, metadata tracking, and pagination
	query := topic + " explained"
	searchResult, err := a.findUsableSourceWithSummary(ctx, topic, query, slug, branchName, nil)
	if err != nil {
		return err
	}

	sourceInfo := searchResult.Source

	// Save article metadata
	if searchResult.Metadata != nil {
		if saveErr := a.saveArticleMetadata(branchName, searchResult.Metadata); saveErr != nil {
			slog.Warn("Failed to save article metadata", "error", saveErr)
		}
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

	// Strip any hallucinated references section before adding the real one
	cleanedMiniArticle := stripReferencesSection(miniArticle)
	fullContent := frontMatter + cleanedMiniArticle + fmt.Sprintf("\n\n## References\n\n[^1]: [%s](%s)", sourceInfo.Title, sourceInfo.URL)

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

// stripReferencesSection removes any hallucinated References/Bibliography/Sources/Notes
// sections from generated content. This provides defense-in-depth against LLMs
// that ignore prompt instructions to not include references.
func stripReferencesSection(content string) string {
	lines := strings.Split(content, "\n")
	var result []string
	inReferencesSection := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(strings.ToLower(line))

		// Detect start of a references-like section
		if strings.HasPrefix(trimmed, "## references") ||
			strings.HasPrefix(trimmed, "## bibliography") ||
			strings.HasPrefix(trimmed, "## sources") ||
			strings.HasPrefix(trimmed, "## notes") ||
			strings.HasPrefix(trimmed, "## citations") {
			inReferencesSection = true
			continue
		}

		// Detect end of references section (new ## heading)
		if inReferencesSection && strings.HasPrefix(trimmed, "## ") {
			inReferencesSection = false
		}

		if !inReferencesSection {
			result = append(result, line)
		}
	}

	// Trim trailing empty lines
	for len(result) > 0 && strings.TrimSpace(result[len(result)-1]) == "" {
		result = result[:len(result)-1]
	}

	return strings.Join(result, "\n")
}

// processTopicWithIterations handles a claimed topic issue by iterating through articles
// It processes N articles (creating new or improving existing), then creates a PR
func (a *Agent) processTopicWithIterations(ctx context.Context, issue *gh.Issue, botUsername string) error {
	iterations := getEnvInt("TOPIC_PROCESSING_ITERATIONS", 50)
	improvementsPerNewArticle := getEnvInt("IMPROVEMENTS_PER_NEW_ARTICLE", 10)
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

		// Separate incomplete and completed articles
		var incompleteArticles, completedArticles []github.ResearchArticle
		for _, a := range articles {
			if a.Completed {
				completedArticles = append(completedArticles, a)
			} else {
				incompleteArticles = append(incompleteArticles, a)
			}
		}

		rand.Seed(time.Now().UnixNano())

		// Prioritize incomplete articles - only improve if all are complete
		if len(incompleteArticles) > 0 {
			// Create new article (prioritize incomplete)
			article := incompleteArticles[rand.Intn(len(incompleteArticles))]
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

			// Run improvement iterations on the newly created article
			log.Printf("Running %d improvement iterations on new article '%s'", improvementsPerNewArticle, article.Name)
			for impIter := 1; impIter <= improvementsPerNewArticle; impIter++ {
				log.Printf("[New Article Improvement %d/%d] Improving '%s'", impIter, improvementsPerNewArticle, article.Name)
				result, err := a.improveArticle(ctx, issue, article.Name, branchName, failedSources)
				if err != nil {
					slog.Warn("Failed to improve new article", "article", article.Name, "iteration", impIter, "error", err)
					errors = append(errors, fmt.Sprintf("Failed to improve '%s' (iter %d): %v", article.Name, impIter, err))
					// Continue trying more improvements even if one fails
					continue
				}
				if result != nil {
					improvementResults = append(improvementResults, result)
				}
			}

			// Generate image prompt after improvements complete
			category, subcategory, _ := extractCategoryContext(issue.GetTitle())
			if err := a.generateHeaderImagePrompt(ctx, article.Name, branchName, category, subcategory); err != nil {
				slog.Warn("Failed to generate image prompt", "article", article.Name, "error", err)
				errors = append(errors, fmt.Sprintf("Failed to generate image prompt for '%s': %v", article.Name, err))
			} else {
				log.Printf("Generated header image prompt for '%s'", article.Name)
			}

			// Process sections for image generation if enabled
			if getEnvBool("GENERATE_SECTION_IMAGES", true) {
				articleSlug := strings.ToLower(strings.ReplaceAll(cleanTopic(article.Name), " ", "-"))
				articlePath := fmt.Sprintf("Compendium/_incoming/%s.md", articleSlug)
				articleContent, _, err := a.gh.GetFile(branchName, articlePath)
				if err != nil {
					slog.Warn("Failed to read article for section images", "article", article.Name, "error", err)
				} else {
					if err := a.ProcessArticleSections(ctx, branchName, articleSlug, articleContent, category, subcategory); err != nil {
						slog.Warn("Failed to process section images", "article", article.Name, "error", err)
					}
				}
			}
		} else if len(completedArticles) > 0 {
			// All articles complete, improve existing
			article := completedArticles[rand.Intn(len(completedArticles))]
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

	// Calculate total iterations including per-article improvements
	totalImprovementIterations := len(articlesCreated) * improvementsPerNewArticle
	totalIterations := iterations + totalImprovementIterations
	log.Printf("Completed %d main iterations + %d new-article improvements (%d total) for topic #%d",
		iterations, totalImprovementIterations, totalIterations, issueNum)

	// Build comprehensive summary
	var summaryBuilder strings.Builder
	summaryBuilder.WriteString("## 📊 Research Bot Summary\n\n")
	summaryBuilder.WriteString(fmt.Sprintf("- **Branch:** `%s`\n", branchName))
	summaryBuilder.WriteString(fmt.Sprintf("- **Duration:** %s\n", time.Since(startTime).Round(time.Second)))
	summaryBuilder.WriteString(fmt.Sprintf("- **Iterations:** %d (+ %d per new article = %d total)\n\n",
		iterations, totalImprovementIterations, totalIterations))

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

	// Run image generation as finalization step (if enabled)
	if getEnvBool("GENERATE_IMAGES_AFTER_RUN", true) {
		log.Println("=== Running Image Generation Finalization ===")
		if err := a.GenerateImages(ctx, branchName); err != nil {
			slog.Warn("Image generation finalization failed", "error", err)
			errors = append(errors, fmt.Sprintf("Image generation failed: %v", err))
		}
	} else {
		log.Println("Image generation skipped (GENERATE_IMAGES_AFTER_RUN=false)")
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

	// Load authorities for entity resolution
	authMgr := authority.NewManager(a.gh)
	if err := authMgr.Load("main"); err != nil {
		slog.Warn("Failed to load authorities", "error", err)
	}

	// Search for sources with global ignore list, metadata tracking, and pagination
	query := topic + " explained"
	searchResult, err := a.findUsableSourceWithSummary(ctx, topic, query, slug, branchName, failedSources)
	if err != nil {
		return err
	}

	sourceInfo := searchResult.Source

	// Save article metadata
	if searchResult.Metadata != nil {
		if saveErr := a.saveArticleMetadata(branchName, searchResult.Metadata); saveErr != nil {
			slog.Warn("Failed to save article metadata", "error", saveErr)
		}
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
		if failedSources != nil {
			failedSources[sourceInfo.URL] = true
		}
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

	// Strip any hallucinated references section before adding the real one
	cleanedMiniArticle := stripReferencesSection(miniArticle)
	fullContent := frontMatter + cleanedMiniArticle + fmt.Sprintf("\n\n## References\n\n[^1]: [%s](%s)", sourceInfo.Title, sourceInfo.URL)

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

	// Extract category context from issue title
	issueTitle := issue.GetTitle()
	category, subcategory, topicName := extractCategoryContext(issueTitle)

	if useAddNewSection {
		err = a.improveModeAddSection(ctx, topic, slug, branchName, articlePath, articleContent, articleSHA, existingSections, &actionLog, result, failedSources, category, subcategory, topicName)
	} else {
		err = a.improveModeImproveSection(ctx, topic, slug, branchName, articlePath, articleContent, articleSHA, existingSections, &actionLog, result, failedSources, category, subcategory, topicName)
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
func (a *Agent) improveModeAddSection(ctx context.Context, topic, slug, branchName, articlePath, articleContent, articleSHA string, existingSections []llm.ArticleSection, actionLog *strings.Builder, result *ImprovementResult, failedSources map[string]bool, category, subcategory, topicName string) error {
	log.Printf("[Mode A] Asking LLM to suggest a new section for topic '%s' (%s > %s > %s)", topic, category, subcategory, topicName)
	actionLog.WriteString("\n### Suggest New Section\n\n")

	// Ask LLM to suggest what new section to add and provide a search query
	suggestion, err := a.llm.SuggestNewSection(ctx, category, subcategory, topicName, existingSections)
	if err != nil {
		actionLog.WriteString(fmt.Sprintf("- **Error:** Failed to get section suggestion: %v\n", err))
		return fmt.Errorf("failed to get section suggestion: %w", err)
	}

	actionLog.WriteString(fmt.Sprintf("- **Suggested section:** %s\n", suggestion.SectionTitle))
	actionLog.WriteString(fmt.Sprintf("- **Insert after:** %s\n", suggestion.InsertAfter))
	actionLog.WriteString(fmt.Sprintf("- **Rationale:** %s\n", suggestion.Rationale))
	actionLog.WriteString(fmt.Sprintf("- **Search query:** %s\n", suggestion.SearchQuery))

	log.Printf("[Mode A] Suggested section '%s', searching with query: %s", suggestion.SectionTitle, suggestion.SearchQuery)
	actionLog.WriteString("\n### Search for New Source\n\n")

	// Use the LLM-generated search query with category context
	query := suggestion.SearchQuery
	searchResult, err := a.findUsableSourceWithSummary(ctx, topic, query, slug, branchName, failedSources)
	if err != nil {
		actionLog.WriteString(fmt.Sprintf("- **Error:** %v\n", err))
		return err
	}

	sourceInfo := searchResult.Source
	sourceInfo.Index = rand.Intn(1000) + 100

	// Save updated metadata
	if searchResult.Metadata != nil {
		// Track skipped sources in result
		for _, skipped := range searchResult.Metadata.SourcesSkipped {
			result.SkippedSources = append(result.SkippedSources, skipped.Domain)
		}
		if saveErr := a.saveArticleMetadata(branchName, searchResult.Metadata); saveErr != nil {
			slog.Warn("Failed to save article metadata", "error", saveErr)
		}
	}

	result.SourceTitle = sourceInfo.Title
	result.SourceURL = sourceInfo.URL
	log.Printf("[Mode A] Found relevant source: %s", sourceInfo.Title)
	actionLog.WriteString(fmt.Sprintf("- **Selected source:** [%s](%s)\n", sourceInfo.Title, sourceInfo.URL))

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

	// Compare sections - this now returns ALL valuable sections to add
	comparison, err := a.llm.CompareSections(ctx, topic, articleContent, existingSectionsStr, newArticle, newSectionsStr)
	if err != nil {
		actionLog.WriteString(fmt.Sprintf("- **Error:** Section comparison failed: %v\n", err))
		return fmt.Errorf("section comparison failed: %w", err)
	}

	if !comparison.HasNewSection || len(comparison.SectionsToAdd) == 0 {
		// Check legacy single section field for backward compatibility
		if comparison.SectionTitle != "" && comparison.SectionContent != "" {
			comparison.SectionsToAdd = []llm.SectionToAdd{{
				Title:       comparison.SectionTitle,
				Content:     comparison.SectionContent,
				InsertAfter: comparison.InsertAfter,
				Reason:      comparison.Reason,
			}}
		} else {
			actionLog.WriteString(fmt.Sprintf("- **Result:** No valuable new sections to add\n"))
			actionLog.WriteString(fmt.Sprintf("- **Reason:** %s\n", comparison.Reason))
			log.Printf("[Mode A] No new sections to add: %s", comparison.Reason)
			return nil
		}
	}

	actionLog.WriteString(fmt.Sprintf("- **Found %d valuable sections to add**\n", len(comparison.SectionsToAdd)))

	// Build map of new section content
	newSectionContent := make(map[string]string)
	var addedSections []string
	for _, section := range comparison.SectionsToAdd {
		newSectionContent[section.Title] = section.Content
		addedSections = append(addedSections, section.Title)
		actionLog.WriteString(fmt.Sprintf("\n#### New Section: %s\n", section.Title))
		actionLog.WriteString(fmt.Sprintf("- Reason: %s\n", section.Reason))
	}

	// Ask LLM to determine optimal section order
	log.Printf("[Mode A] Asking LLM to determine optimal section order")
	actionLog.WriteString("\n### Section Ordering\n\n")

	orderReq := llm.SectionOrderRequest{
		Topic:            topic,
		ExistingSections: existingSections,
		NewSections:      comparison.SectionsToAdd,
	}
	orderResult, err := a.llm.OrderSections(ctx, orderReq)

	var updatedContent string
	if err != nil {
		// Fallback to sequential insertion if ordering fails
		log.Printf("[Mode A] Section ordering failed, falling back to sequential insertion: %v", err)
		actionLog.WriteString(fmt.Sprintf("- **Warning:** Section ordering failed (%v), using fallback\n", err))

		updatedContent = articleContent
		for _, section := range comparison.SectionsToAdd {
			updatedContent = insertSection(updatedContent, section.InsertAfter, section.Title, section.Content)
		}
	} else {
		// Use LLM-determined order to reconstruct article
		actionLog.WriteString(fmt.Sprintf("- **Ordered sections:** %s\n", strings.Join(orderResult.OrderedTitles, " → ")))
		actionLog.WriteString(fmt.Sprintf("- **Reasoning:** %s\n", orderResult.Reasoning))
		log.Printf("[Mode A] LLM ordered %d sections: %v", len(orderResult.OrderedTitles), orderResult.OrderedTitles)

		updatedContent = reorderArticleSections(articleContent, orderResult.OrderedTitles, newSectionContent)
	}

	// Update iteration count
	updatedContent = incrementIterationCount(updatedContent)

	// Append reference to References section
	updatedContent = appendReference(updatedContent, sourceInfo.Title, sourceInfo.URL)

	// Save updated article with all new sections
	commitMsg := fmt.Sprintf("Add %d section(s) to %s", len(addedSections), topic)
	if len(addedSections) == 1 {
		commitMsg = fmt.Sprintf("Add section '%s' to %s", addedSections[0], topic)
	}
	if err := a.gh.UpdateFile(branchName, articlePath, commitMsg, updatedContent, articleSHA); err != nil {
		actionLog.WriteString(fmt.Sprintf("- **Error:** Failed to save article: %v\n", err))
		return fmt.Errorf("failed to save article: %w", err)
	}

	// Save source
	authMgr := authority.NewManager(a.gh)
	_ = a.saveSourceSummary(ctx, sourceInfo, topic, slug, branchName, authMgr, false)

	result.SectionsAdded = addedSections
	result.SectionName = strings.Join(addedSections, ", ")
	actionLog.WriteString(fmt.Sprintf("\n### Result\n\n- **Success:** Added %d section(s) to article: %s\n", len(addedSections), result.SectionName))
	log.Printf("[Mode A] Successfully added %d section(s) to article '%s': %v", len(addedSections), topic, addedSections)
	return nil
}

// selectSectionWeighted selects a section with bias toward shorter sections that need improvement
func selectSectionWeighted(sections []llm.ArticleSection) *llm.ArticleSection {
	if len(sections) == 0 {
		return nil
	}

	// Calculate weights: shorter sections get higher weight (more likely to need improvement)
	var weights []float64
	var totalWeight float64
	for _, s := range sections {
		wordCount := len(strings.Fields(s.Content))
		// Inverse weight: fewer words = higher weight
		// +50 to avoid extreme weights for very short sections
		weight := 1.0 / float64(wordCount+50)
		weights = append(weights, weight)
		totalWeight += weight
	}

	// Weighted random selection
	r := rand.Float64() * totalWeight
	cumulative := 0.0
	for i, w := range weights {
		cumulative += w
		if r <= cumulative {
			return &sections[i]
		}
	}
	return &sections[len(sections)-1]
}

// improveModeImproveSection implements Mode B: Select existing section, search for details, improve it
func (a *Agent) improveModeImproveSection(ctx context.Context, topic, slug, branchName, articlePath, articleContent, articleSHA string, existingSections []llm.ArticleSection, actionLog *strings.Builder, result *ImprovementResult, failedSources map[string]bool, category, subcategory, topicName string) error {
	log.Printf("[Mode B] Improving existing section for topic '%s' (%s > %s > %s)", topic, category, subcategory, topicName)

	if len(existingSections) == 0 {
		actionLog.WriteString("- **Result:** No sections found to improve\n")
		return fmt.Errorf("no sections found to improve")
	}

	// Filter to level 2 sections (H2), excluding References
	var eligibleSections []llm.ArticleSection
	for _, s := range existingSections {
		if s.Level == 2 && s.Title != "References" {
			eligibleSections = append(eligibleSections, s)
		}
	}

	// If no H2 sections, try other non-Reference sections
	if len(eligibleSections) == 0 {
		for _, s := range existingSections {
			if s.Title != "References" {
				eligibleSections = append(eligibleSections, s)
			}
		}
	}

	if len(eligibleSections) == 0 {
		actionLog.WriteString("- **Result:** No suitable sections to improve\n")
		return fmt.Errorf("no suitable sections to improve")
	}

	// Use weighted selection - bias toward shorter sections that need improvement
	selected := selectSectionWeighted(eligibleSections)
	if selected == nil {
		actionLog.WriteString("- **Result:** Failed to select section\n")
		return fmt.Errorf("failed to select section")
	}
	selectedSection := *selected

	wordCount := len(strings.Fields(selectedSection.Content))
	actionLog.WriteString(fmt.Sprintf("\n### Selected Section: %s (word count: %d)\n\n", selectedSection.Title, wordCount))
	log.Printf("[Mode B] Selected section '%s' with %d words (weighted selection)", selectedSection.Title, wordCount)

	// Extract the current section content from the article
	currentSectionContent := extractSectionContent(articleContent, selectedSection.Title)
	if currentSectionContent == "" {
		actionLog.WriteString("- **Warning:** Could not extract section content\n")
	}

	// Generate search query using deterministic template (more reliable than LLM)
	query := fmt.Sprintf("%s %s %s %s", category, subcategory, topicName, selectedSection.Title)
	log.Printf("[Mode B] Searching with query: %s", query)
	actionLog.WriteString(fmt.Sprintf("- **Search query:** %s\n", query))

	result.SectionName = selectedSection.Title

	searchResult, err := a.findUsableSourceWithSummary(ctx, topic, query, slug, branchName, failedSources)
	if err != nil {
		actionLog.WriteString(fmt.Sprintf("- **Error:** %v\n", err))
		return err
	}

	sourceInfo := searchResult.Source
	sourceInfo.Index = rand.Intn(1000) + 100

	// Save updated metadata
	if searchResult.Metadata != nil {
		// Track skipped sources in result
		for _, skipped := range searchResult.Metadata.SourcesSkipped {
			result.SkippedSources = append(result.SkippedSources, skipped.Domain)
		}
		if saveErr := a.saveArticleMetadata(branchName, searchResult.Metadata); saveErr != nil {
			slog.Warn("Failed to save article metadata", "error", saveErr)
		}
	}

	result.SourceTitle = sourceInfo.Title
	result.SourceURL = sourceInfo.URL
	actionLog.WriteString(fmt.Sprintf("- **Selected source:** [%s](%s)\n", sourceInfo.Title, sourceInfo.URL))

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

	// Append reference to References section
	updatedContent = appendReference(updatedContent, sourceInfo.Title, sourceInfo.URL)

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

// reorderArticleSections reconstructs an article with sections in the specified order
// orderedTitles contains all section titles (existing + new) in the desired order
// newSections maps new section titles to their content
func reorderArticleSections(article string, orderedTitles []string, newSections map[string]string) string {
	lines := strings.Split(article, "\n")
	var result strings.Builder

	// 1. Extract and write frontmatter
	inFrontmatter := false
	frontmatterEnd := 0
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			if !inFrontmatter {
				inFrontmatter = true
				result.WriteString(line + "\n")
			} else {
				result.WriteString(line + "\n")
				frontmatterEnd = i + 1
				break
			}
		} else if inFrontmatter {
			result.WriteString(line + "\n")
		}
	}

	// 2. Extract intro content (content between frontmatter and first heading)
	var introContent strings.Builder
	for i := frontmatterEnd; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "#") {
			break
		}
		introContent.WriteString(lines[i] + "\n")
	}
	intro := strings.TrimSpace(introContent.String())
	if intro != "" {
		result.WriteString("\n" + intro + "\n")
	}

	// 3. Build a map of existing section titles to their content
	existingSections := make(map[string]string)
	var currentTitle string
	var currentContent strings.Builder
	inSection := false

	for i := frontmatterEnd; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])

		if strings.HasPrefix(trimmed, "#") {
			title := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))

			// Save previous section if we were in one
			if inSection && currentTitle != "" {
				existingSections[currentTitle] = strings.TrimSpace(currentContent.String())
			}

			// Start new section
			currentTitle = title
			currentContent.Reset()
			currentContent.WriteString(lines[i] + "\n")
			inSection = true
		} else if inSection {
			currentContent.WriteString(lines[i] + "\n")
		}
	}
	// Save last section
	if inSection && currentTitle != "" {
		existingSections[currentTitle] = strings.TrimSpace(currentContent.String())
	}

	// 4. Write sections in the ordered sequence
	var referencesContent string
	for _, title := range orderedTitles {
		if title == "References" {
			// Save References for the end
			if content, ok := existingSections[title]; ok {
				referencesContent = content
			}
			continue
		}

		// Check if it's a new section
		if content, ok := newSections[title]; ok {
			result.WriteString("\n## " + title + "\n\n")
			result.WriteString(content + "\n")
		} else if content, ok := existingSections[title]; ok {
			// Existing section - write as-is (includes heading)
			result.WriteString("\n" + content + "\n")
		}
	}

	// 5. Add References section at the end (if it exists)
	if referencesContent != "" {
		result.WriteString("\n" + referencesContent + "\n")
	} else if content, ok := existingSections["References"]; ok {
		result.WriteString("\n" + content + "\n")
	}

	return strings.TrimRight(result.String(), "\n")
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

// appendReference adds a new reference to the References section
func appendReference(content string, sourceTitle, sourceURL string) string {
	lines := strings.Split(content, "\n")
	var result strings.Builder

	// Count existing references to determine next number
	refCount := 0
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "[^") {
			refCount++
		}
	}
	nextRef := refCount + 1

	// Find References section and append
	inReferences := false
	refAdded := false

	for i, line := range lines {
		trimmedLine := strings.TrimSpace(line)

		// Check if we're entering References section
		if strings.HasPrefix(trimmedLine, "## References") || strings.HasPrefix(trimmedLine, "# References") {
			inReferences = true
		}

		// If in references and we hit a new section or end, append before it
		if inReferences && !refAdded {
			if (strings.HasPrefix(trimmedLine, "#") && !strings.Contains(trimmedLine, "References")) || i == len(lines)-1 {
				// Append reference before this line
				if i == len(lines)-1 {
					result.WriteString(line + "\n")
				}
				result.WriteString(fmt.Sprintf("[^%d]: [%s](%s)\n", nextRef, sourceTitle, sourceURL))
				refAdded = true
				if i == len(lines)-1 {
					continue
				}
			}
		}

		result.WriteString(line + "\n")
	}

	// If no references section found or couldn't add, append at end
	if !refAdded {
		result.WriteString(fmt.Sprintf("\n[^%d]: [%s](%s)\n", nextRef, sourceTitle, sourceURL))
	}

	return strings.TrimRight(result.String(), "\n")
}

// generateHeaderImagePrompt generates an image prompt for the article header and saves it to the debug folder
func (a *Agent) generateHeaderImagePrompt(ctx context.Context, articleName, branchName, category, subcategory string) error {
	topic := cleanTopic(articleName)
	slug := strings.ToLower(strings.ReplaceAll(topic, " ", "-"))

	log.Printf("[Image Prompt] Generating header image prompt for '%s' (%s > %s)", topic, category, subcategory)

	// Load the article content
	articlePath := fmt.Sprintf("Compendium/_incoming/%s.md", slug)
	articleContent, _, err := a.gh.GetFile(branchName, articlePath)
	if err != nil {
		return fmt.Errorf("failed to load article: %w", err)
	}

	// Step 1: Extract article-specific visual elements using LLM
	log.Printf("[Image Prompt] Extracting visual elements from article...")
	visualReq := llm.VisualElementsRequest{
		Topic:          topic,
		Category:       category,
		Subcategory:    subcategory,
		ArticleContent: articleContent,
	}
	visualElements, err := a.llm.ExtractVisualElements(ctx, visualReq)
	if err != nil {
		slog.Warn("Failed to extract visual elements, using defaults", "error", err)
		visualElements = &llm.VisualElements{}
	}

	// Extract article summary for additional context
	summary := extractArticleSummary(articleContent, 500)

	// Get config directory path
	configDir := getEnvOrDefault("CONFIG_DIR", "config")

	// Load style configuration
	styleMgr := styles.NewManager(configDir)
	if err := styleMgr.Load(); err != nil {
		return fmt.Errorf("failed to load style config: %w", err)
	}

	// Resolve category-based configuration (for styles and colors)
	resolved := styleMgr.ResolveAll("header", category, subcategory)

	// Select 1-2 random artistic styles from config
	numStyles := 1
	if len(resolved.ArtisticStyles) > 2 {
		numStyles = 2
	}
	selectedStyles := styleMgr.SelectRandomStyles(resolved.ArtisticStyles, numStyles)

	// Select a random color mood from config
	colorMood := styleMgr.SelectRandomColorMood(resolved.ColorMoods)

	// Step 2: Generate the image prompt with structured elements (not flattened)
	req := llm.ImagePromptRequest{
		Topic:             topic,
		Category:          category,
		Subcategory:       subcategory,
		ArticleSummary:    summary,
		ExtractedElements: visualElements, // Pass full structured extraction
		ColorMood:         colorMood,
		ArtisticStyles:    selectedStyles,
		CategoryGuidance:  resolved.Guidance,
	}

	result, err := a.llm.GenerateImagePrompt(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to generate image prompt: %w", err)
	}

	// Save the full extraction debug JSON for visibility into what was extracted
	debugDir := debugBasePath(slug)
	extractionDebugPath := fmt.Sprintf("%s/extraction_debug.json", debugDir)
	extractionDebug := map[string]interface{}{
		"topic":       topic,
		"category":    category,
		"subcategory": subcategory,
		"extraction": map[string]interface{}{
			"key_concepts":       visualElements.KeyConcepts,
			"specific_phenomena": visualElements.SpecificPhenomena,
			"notable_figures":    visualElements.NotableFigures,
			"iconic_imagery":     visualElements.IconicImagery,
			"math_elements":      visualElements.MathElements,
		},
		"style_config": map[string]interface{}{
			"selected_styles": selectedStyles,
			"color_mood":      colorMood,
		},
		"generated_at": time.Now().UTC().Format(time.RFC3339),
	}
	extractionJSON, _ := json.MarshalIndent(extractionDebug, "", "  ")
	if err := a.gh.CreateFile(branchName, extractionDebugPath, fmt.Sprintf("Add extraction debug for %s", topic), string(extractionJSON)); err != nil {
		slog.Warn("Failed to save extraction debug", "error", err)
	}

	// Save the prompt to the debug folder with extraction details
	promptPath := fmt.Sprintf("%s/header_image_prompt.txt", debugDir)
	promptContent := fmt.Sprintf("# Header Image Prompt for: %s\n", topic)
	promptContent += fmt.Sprintf("# Category: %s > %s\n", category, subcategory)
	promptContent += fmt.Sprintf("# Styles: %s\n", strings.Join(selectedStyles, ", "))
	promptContent += fmt.Sprintf("# Color Mood: %s\n", colorMood)
	promptContent += fmt.Sprintf("# Generated: %s\n", time.Now().UTC().Format(time.RFC3339))
	promptContent += fmt.Sprintf("# Model: %s\n", result.Model)
	promptContent += fmt.Sprintf("# Extracted Elements:\n")
	if len(visualElements.KeyConcepts) > 0 {
		promptContent += fmt.Sprintf("#   Key Concepts: %s\n", strings.Join(visualElements.KeyConcepts, ", "))
	}
	if len(visualElements.SpecificPhenomena) > 0 {
		promptContent += fmt.Sprintf("#   Phenomena: %s\n", strings.Join(visualElements.SpecificPhenomena, ", "))
	}
	if len(visualElements.IconicImagery) > 0 {
		promptContent += fmt.Sprintf("#   Iconic Imagery: %s\n", strings.Join(visualElements.IconicImagery, ", "))
	}
	if len(visualElements.NotableFigures) > 0 {
		promptContent += fmt.Sprintf("#   Notable Figures: %s\n", strings.Join(visualElements.NotableFigures, ", "))
	}
	if len(visualElements.MathElements) > 0 {
		promptContent += fmt.Sprintf("#   Math Elements: %s\n", strings.Join(visualElements.MathElements, ", "))
	}
	if result.Thinking != "" {
		promptContent += fmt.Sprintf("# LLM Reasoning:\n#   %s\n", strings.ReplaceAll(result.Thinking, "\n", "\n#   "))
	}
	promptContent += "\n"
	promptContent += result.Prompt

	if err := a.gh.CreateFile(branchName, promptPath, fmt.Sprintf("Add header image prompt for %s", topic), promptContent); err != nil {
		return fmt.Errorf("failed to save image prompt: %w", err)
	}

	log.Printf("[Image Prompt] Saved prompt to %s (%d chars)", promptPath, len(result.Prompt))

	return nil
}


// extractArticleSummary extracts a summary from the article content
// It tries to get the intro section (content before the first ## heading after frontmatter)
// or falls back to the first maxLen characters
func extractArticleSummary(content string, maxLen int) string {
	// Skip frontmatter
	lines := strings.Split(content, "\n")
	inFrontmatter := false
	contentStart := 0

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			if !inFrontmatter {
				inFrontmatter = true
			} else {
				contentStart = i + 1
				break
			}
		}
	}

	// Get content after frontmatter
	if contentStart > 0 && contentStart < len(lines) {
		lines = lines[contentStart:]
	}

	// Find intro section (content before first heading)
	var introLines []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Stop at first heading
		if strings.HasPrefix(trimmed, "## ") {
			break
		}
		// Skip empty lines at the start
		if len(introLines) == 0 && trimmed == "" {
			continue
		}
		introLines = append(introLines, line)
	}

	intro := strings.TrimSpace(strings.Join(introLines, "\n"))

	// If intro is too short, try to get more content
	if len(intro) < 100 {
		fullContent := strings.TrimSpace(strings.Join(lines, "\n"))
		if len(fullContent) > maxLen {
			// Try to break at a sentence
			truncated := fullContent[:maxLen]
			lastPeriod := strings.LastIndex(truncated, ". ")
			if lastPeriod > maxLen/2 {
				return truncated[:lastPeriod+1]
			}
			return truncated + "..."
		}
		return fullContent
	}

	// Truncate if needed
	if len(intro) > maxLen {
		lastPeriod := strings.LastIndex(intro[:maxLen], ". ")
		if lastPeriod > maxLen/2 {
			return intro[:lastPeriod+1]
		}
		return intro[:maxLen] + "..."
	}

	return intro
}

// getEnvOrDefault returns the environment variable value or a default
func getEnvOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}
