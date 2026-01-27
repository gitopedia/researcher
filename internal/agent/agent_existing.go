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

	"github.com/gitopedia/researcher/internal/github"
)

type SourceInfo struct {
	Index   int
	URL     string
	Title   string
	Summary string
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

		srcInfo := SourceInfo{
			Index:   rand.Intn(1000) + 100,
			URL:     r.Href,
			Title:   r.Title,
			Summary: mini,
		}

		slug := strings.TrimSuffix(filepath.Base(articlePath), ".md")
		if err := a.saveSourceSummary(srcInfo, topic, slug, branchName); err != nil {
			continue
		}

		return nil
	}

	return fmt.Errorf("no new sources found")
}
