package agent

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"math/rand"
	"path/filepath"
	"strings"
	"time"

	"github.com/gitopedia/researcher/internal/authority"
	"github.com/gitopedia/researcher/internal/github"
)

type SourceInfo struct {
	Index   int
	URL     string
	Title   string
	Summary string
}

// processExistingPR builds on an existing draft PR
func (a *Agent) processExistingPR(ctx context.Context, pr *github.PRInfo) error {
	log.Printf("Processing Existing PR: #%d (%s)", pr.Number, pr.Title)

	// 1. Checkout Branch
	branchName := pr.HeadBranch
	// In a real git env we would checkout, but here we work with GitHub API directly.
	// We just need to know the branch name for operations.

	// 2. Load Article Content
	// We need to find the article file in the branch.
	// It should be in Compendium/_incoming/{slug}.md
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

	// Parse slug and topic from content/path
	// slug := strings.TrimSuffix(filepath.Base(articlePath), ".md") // Not used
	// We can try to parse topic from frontmatter or just use title
	// For now, let's extract title from frontmatter or PR title
	topic := pr.Title // "Init Category: Topic" -> we need to parse again?
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
		// Check if used in article (naive check: is the source ID or URL referenced?)
		// Better: check frontmatter of source for "used: true" or similar?
		// Or check if the article contains citation to it?
		// For now, let's just pick one random source and see if we can add something from it.
		// To properly track "used", we should update source frontmatter.
		
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
		// Path A: Use existing source
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

	// Parse source content (remove frontmatter)
	srcBody := srcContent
	if strings.HasPrefix(srcBody, "---") {
		parts := strings.SplitN(srcBody, "---", 3)
		if len(parts) == 3 {
			srcBody = parts[2]
		}
	}

	// Check Relevance
	log.Println("Checking relevance...")
	resultRelevance, err := a.llm.CheckRelevance(ctx, topic, srcBody)
	if err != nil {
		slog.Warn("Relevance check failed", "error", err)
		// Assume relevant if check fails? Or skip?
		// Let's skip to be safe
		return fmt.Errorf("relevance check failed: %w", err)
	}
	if !resultRelevance.Relevant {
		log.Printf("Source rejected: %s (Score: %.1f)", resultRelevance.Reason, 0.0) // Score not available in struct
		// Mark as used/rejected so we don't try again
		return a.markSourceAsUsed(branchName, srcPath, srcContent, srcSHA, "rejected: "+resultRelevance.Reason)
	}

	// Check Redundancy
	log.Println("Checking redundancy...")
	resultRedundancy, err := a.llm.CheckRedundancy(ctx, topic, articleContent, srcBody)
	if err != nil {
		slog.Warn("Redundancy check failed", "error", err)
	} else if resultRedundancy.IsRedundant {
		log.Printf("Source redundant: %s", resultRedundancy.Reason)
		return a.markSourceAsUsed(branchName, srcPath, srcContent, srcSHA, "redundant")
	}

	// Integrate
	log.Println("Integrating content...")
	newArticleContent, err := a.llm.IntegrateContent(ctx, topic, articleContent, srcBody)
	if err != nil {
		return fmt.Errorf("integration failed: %w", err)
	}

	// Update Article
	log.Println("Updating article...")
	if err := a.gh.UpdateFile(branchName, articlePath, "Update article with new source", newArticleContent, articleSHA); err != nil {
		return fmt.Errorf("failed to update article: %w", err)
	}

	// Mark source as used
	return a.markSourceAsUsed(branchName, srcPath, srcContent, srcSHA, "integrated")
}

func (a *Agent) markSourceAsUsed(branchName, srcPath, content, sha, status string) error {
	// Inject used_in_version field into frontmatter
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
	// Simple implementation: Just search for the topic again, maybe with a sub-topic from article?
	// For now, generic search
	log.Printf("Fetching new source for: %s", topic)
	
	// Generate a search query based on article content gaps?
	// Or just random term
	query := topic + " details facts" 
	results, err := a.search.Search(query)
	if err != nil {
		return err
	}

	// Find a result not already in sources (we'd need to list sources first, but let's rely on random chance + high count)
	// Better: check against existing source URLs.
	// Assume we fetch one and if it's new, save it.
	
	for _, r := range results {
		if strings.HasSuffix(r.Href, ".pdf") {
			continue
		}
		
		// Check if URL already exists in sources (filename hash or content?)
		// We'll skip this check for MVP, but saving duplicate sources is wasteful.
		
		content, err := a.search.FetchContent(r.Href)
		if err != nil {
			continue
		}
		
		// Generate Mini Article
		mini, err := a.llm.GenerateMiniArticle(ctx, topic, r.Title, content)
		if err != nil {
			continue
		}
		
		// Save Source
		authMgr := authority.NewManager(a.gh) // New instance (empty cache is fine for this)
		srcInfo := SourceInfo{
			Index:   rand.Intn(1000) + 100, // Random index for now
			URL:     r.Href,
			Title:   r.Title,
			Summary: mini, // Use mini article as summary
		}
		
		slug := strings.TrimSuffix(filepath.Base(articlePath), ".md")
		if err := a.saveSourceSummary(ctx, srcInfo, topic, slug, branchName, authMgr, false); err != nil {
			slog.Warn("Failed to save new source", "error", err)
			continue
		}
		
		log.Printf("Saved new source: %s", r.Title)
		return nil // Done for this run (incremental)
	}
	
	return fmt.Errorf("no new sources found")
}
