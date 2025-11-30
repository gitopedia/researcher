package agent

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ArticleFrontMatter represents the YAML front matter of an article
type ArticleFrontMatter struct {
	ID      string   `yaml:"id"`
	Title   string   `yaml:"title"`
	Slug    string   `yaml:"slug"`
	Created string   `yaml:"created"`
	Tags    []string `yaml:"tags"`
}

// organizeArticles processes all articles in _incoming/ and moves them to proper Compendium paths
func (a *Agent) organizeArticles(ctx context.Context, branchName string, prNumber int) error {
	log.Printf("Organizing articles in branch %s for PR #%d", branchName, prNumber)

	// 1. List all files in _incoming/ on the branch (excluding sources/)
	incomingPath := "Compendium/_incoming"
	files, err := a.gh.ListFilesInBranch(branchName, incomingPath)
	if err != nil {
		return fmt.Errorf("failed to list files in %s: %w", incomingPath, err)
	}

	// Filter to only .md files in _incoming/ root (not sources/)
	var articles []string
	var debugDirs []string
	for _, f := range files {
		// Skip sources directory
		if strings.Contains(f, "_incoming/sources/") {
			continue
		}
		// Track debug directories for deletion
		if strings.Contains(f, "_debug/") {
			debugDirs = append(debugDirs, f)
			continue
		}
		// Only process .md files directly in _incoming/
		if strings.HasSuffix(f, ".md") && filepath.Dir(f) == incomingPath {
			articles = append(articles, f)
		}
	}

	if len(articles) == 0 {
		log.Println("No articles found in _incoming/ to organize")
		return nil
	}

	log.Printf("Found %d articles to organize", len(articles))

	// 2. Get existing categories from Compendium
	existingCategories, err := a.getExistingCategories(branchName)
	if err != nil {
		slog.Warn("Failed to get existing categories", "error", err)
		existingCategories = []string{}
	}

	// 3. Process each article
	var organized []string
	for _, articlePath := range articles {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		log.Printf("Processing article: %s", articlePath)

		// Read article content
		content, sha, err := a.gh.GetFile(branchName, articlePath)
		if err != nil {
			slog.Error("Failed to read article", "path", articlePath, "error", err)
			continue
		}

		// Parse front matter
		frontMatter, body, err := parseFrontMatter(content)
		if err != nil {
			slog.Error("Failed to parse front matter", "path", articlePath, "error", err)
			continue
		}

		// Validate front matter
		if err := validateFrontMatter(frontMatter, articlePath); err != nil {
			log.Printf("Invalid front matter for %s: %v", articlePath, err)
			continue
		}

		// Check context before LLM call
		if ctx.Err() != nil {
			log.Printf("Context cancelled, stopping organization")
			return ctx.Err()
		}

		// Use LLM to categorize
		category, err := a.llm.CategorizeArticle(ctx, frontMatter.Title, frontMatter.Tags, body, existingCategories)
		if err != nil {
			// Check if it's a context cancellation
			if ctx.Err() != nil {
				log.Printf("Context cancelled during categorization")
				return ctx.Err()
			}
			slog.Error("Failed to categorize article", "path", articlePath, "error", err)
			continue
		}

		log.Printf("Categorized '%s' as: %s (reason: %s)", frontMatter.Title, category.Category, category.Reasoning)

		// Determine new path
		newPath := fmt.Sprintf("Compendium/%s/%s.md", category.Category, frontMatter.Slug)

		// Create file at new location
		commitMsg := fmt.Sprintf("Organize: Move %s to %s", frontMatter.Title, category.Category)
		if err := a.gh.CreateFile(branchName, newPath, commitMsg, content); err != nil {
			slog.Error("Failed to create file", "path", newPath, "error", err)
			// Check for 401 - authentication failure should stop everything
			if strings.Contains(err.Error(), "401") {
				return fmt.Errorf("authentication failed: %w", err)
			}
			continue
		}

		// Delete file from old location
		deleteMsg := fmt.Sprintf("Organize: Remove %s from _incoming", frontMatter.Slug)
		if err := a.gh.DeleteFile(branchName, articlePath, deleteMsg, sha); err != nil {
			slog.Error("Failed to delete old file", "path", articlePath, "error", err)
			// Continue anyway - file was created at new location
		}

		organized = append(organized, fmt.Sprintf("- %s → %s", frontMatter.Title, newPath))
		
		// Add new category to existing list for subsequent articles
		if !contains(existingCategories, category.Category) {
			existingCategories = append(existingCategories, category.Category)
		}
	}

	// 4. Delete debug directories
	if len(debugDirs) > 0 {
		log.Printf("Deleting %d debug files", len(debugDirs))
		for _, debugFile := range debugDirs {
			_, sha, err := a.gh.GetFile(branchName, debugFile)
			if err != nil {
				slog.Warn("Failed to get SHA for debug file", "path", debugFile, "error", err)
				continue
			}
			if err := a.gh.DeleteFile(branchName, debugFile, "Cleanup: Remove debug artifacts", sha); err != nil {
				slog.Warn("Failed to delete debug file", "path", debugFile, "error", err)
			}
		}
	}

	// 5. Add summary comment to PR
	if len(organized) > 0 {
		comment := fmt.Sprintf("📚 **Articles Organized**\n\n%s\n\n✅ Ready for review.", strings.Join(organized, "\n"))
		if err := a.gh.CommentOnPR(prNumber, comment); err != nil {
			slog.Warn("Failed to add summary comment", "error", err)
		}
	}

	// 6. Mark PR as ready
	log.Printf("Marking PR #%d as ready for review", prNumber)
	if err := a.gh.MarkPRReady(prNumber); err != nil {
		slog.Error("Failed to mark PR as ready", "pr", prNumber, "error", err)
		// Don't fail - the PR can still be merged manually
	}

	log.Printf("Successfully organized %d articles", len(organized))
	return nil
}

// getExistingCategories returns a list of existing category paths in Compendium
func (a *Agent) getExistingCategories(branch string) ([]string, error) {
	files, err := a.gh.ListFilesInBranch(branch, "Compendium")
	if err != nil {
		return nil, err
	}

	categorySet := make(map[string]bool)
	for _, f := range files {
		if strings.HasSuffix(f, ".md") && !strings.Contains(f, "_incoming") {
			// Extract category path (e.g., "Compendium/Science/Physics/article.md" -> "Science/Physics")
			parts := strings.Split(f, "/")
			if len(parts) >= 3 {
				// Skip "Compendium" prefix and filename
				categoryPath := strings.Join(parts[1:len(parts)-1], "/")
				if categoryPath != "" {
					categorySet[categoryPath] = true
				}
			}
		}
	}

	var categories []string
	for cat := range categorySet {
		categories = append(categories, cat)
	}
	return categories, nil
}

// parseFrontMatter extracts YAML front matter from markdown content
func parseFrontMatter(content string) (*ArticleFrontMatter, string, error) {
	if !strings.HasPrefix(content, "---") {
		return nil, content, fmt.Errorf("no front matter found")
	}

	// Find end of front matter
	endIndex := strings.Index(content[3:], "---")
	if endIndex == -1 {
		return nil, content, fmt.Errorf("front matter not closed")
	}

	frontMatterStr := content[3 : endIndex+3]
	body := content[endIndex+6:] // Skip closing "---" and newline

	var fm ArticleFrontMatter
	if err := yaml.Unmarshal([]byte(frontMatterStr), &fm); err != nil {
		return nil, body, fmt.Errorf("failed to parse YAML: %w", err)
	}

	return &fm, body, nil
}

// validateFrontMatter checks that required fields are present and valid
func validateFrontMatter(fm *ArticleFrontMatter, path string) error {
	if fm.ID == "" {
		return fmt.Errorf("missing id")
	}
	if len(fm.ID) != 26 {
		return fmt.Errorf("invalid ULID: expected 26 chars, got %d", len(fm.ID))
	}
	if fm.Title == "" {
		return fmt.Errorf("missing title")
	}
	if fm.Slug == "" {
		return fmt.Errorf("missing slug")
	}
	if len(fm.Tags) == 0 {
		return fmt.Errorf("missing tags")
	}
	
	// Check slug matches filename
	expectedSlug := strings.TrimSuffix(filepath.Base(path), ".md")
	if fm.Slug != expectedSlug {
		return fmt.Errorf("slug '%s' doesn't match filename '%s'", fm.Slug, expectedSlug)
	}

	return nil
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

