package agent

import (
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/gitopedia/researcher/internal/github"
)

// ArticleInfo contains parsed frontmatter info for organizing
type ArticleInfo struct {
	Filename       string
	Domain         string
	DomainSlug     string
	Category       string
	CategorySlug   string
	Topic          string
	TopicSlug      string
	Article        string
	ArticleSlug    string
	GithubIssueIDs []int
	Content        string
	SHA            string
}

// OrganizeArticlesOnBranch is a public method to organize articles on a specific branch
// Can be called via CLI with --organize flag
func (a *Agent) OrganizeArticlesOnBranch(branchName string) error {
	pr := &github.PRInfo{
		HeadBranch: branchName,
		Number:     0,
	}
	return a.organizeIncomingArticles(pr)
}

// organizeIncomingArticles moves articles from _incoming to their correct locations
// and updates index files as needed. This should be called before PR merge.
func (a *Agent) organizeIncomingArticles(pr *github.PRInfo) error {
	branchName := pr.HeadBranch
	log.Printf("Organizing incoming articles for PR #%d on branch %s", pr.Number, branchName)

	// Step 1: Merge main into the branch to ensure it's up to date
	log.Println("Updating PR branch with main...")
	if err := a.gh.UpdatePRBranch(pr.Number); err != nil {
		slog.Warn("Failed to update PR branch with main", "error", err)
		// Continue anyway - might already be up to date
	}

	// Step 2: List all files in _incoming
	incomingPath := "Compendium/_incoming"
	files, err := a.gh.ListFilesInBranch(branchName, incomingPath)
	if err != nil {
		return fmt.Errorf("failed to list incoming files: %w", err)
	}

	// Step 3: Parse and organize articles
	var articles []ArticleInfo
	for _, file := range files {
		// Only process markdown files (not in sources/, not images)
		if !strings.HasSuffix(file, ".md") {
			continue
		}
		if strings.Contains(file, "/sources/") || strings.Contains(file, "/.meta/") {
			continue
		}

		content, sha, err := a.gh.GetFile(branchName, file)
		if err != nil {
			slog.Warn("Failed to get file", "file", file, "error", err)
			continue
		}

		article, err := parseArticleFrontmatter(content)
		if err != nil {
			slog.Warn("Failed to parse frontmatter", "file", file, "error", err)
			continue
		}
		article.Filename = file
		article.Content = content
		article.SHA = sha
		articles = append(articles, article)
	}

	if len(articles) == 0 {
		log.Println("No articles found in _incoming to organize")
		// Still run cleanup even if no articles to move
		log.Println("Cleaning up working folders...")
		a.cleanupWorkingFolders(branchName)
		return nil
	}

	log.Printf("Found %d articles to organize", len(articles))

	// Step 4: Move each article to its correct location
	// Track which paths need index updates
	indexUpdates := make(map[string]bool)

	for _, article := range articles {
		targetDir := fmt.Sprintf("Compendium/%s/%s/%s",
			article.DomainSlug, article.CategorySlug, article.TopicSlug)
		targetPath := fmt.Sprintf("%s/%s.md", targetDir, article.ArticleSlug)

		log.Printf("Moving %s -> %s", article.Filename, targetPath)

		// Check if article already exists at target
		_, _, existsErr := a.gh.GetFile(branchName, targetPath)
		isNew := existsErr != nil

		// Update header image reference in content before moving
		// Change from ![Header](slug_header.png) to ![Header](_img/slug_header.avif)
		updatedContent := updateHeaderImageReference(article.Content, article.ArticleSlug)

		// Create the file at the new location
		if err := a.gh.CreateFile(branchName, targetPath,
			fmt.Sprintf("Move article: %s", article.Article), updatedContent); err != nil {
			slog.Error("Failed to create article at target", "path", targetPath, "error", err)
			continue
		}

		// Delete the original file
		if err := a.gh.DeleteFile(branchName, article.Filename,
			fmt.Sprintf("Remove from _incoming: %s", article.Article), article.SHA); err != nil {
			slog.Warn("Failed to delete original file", "file", article.Filename, "error", err)
		}

		// Mark indexes that need updating if this is a new article
		if isNew {
			indexUpdates["Compendium"] = true
			indexUpdates[fmt.Sprintf("Compendium/%s", article.DomainSlug)] = true
			indexUpdates[fmt.Sprintf("Compendium/%s/%s", article.DomainSlug, article.CategorySlug)] = true
			indexUpdates[fmt.Sprintf("Compendium/%s/%s/%s", article.DomainSlug, article.CategorySlug, article.TopicSlug)] = true
		}
	}

	// Step 5: Update index files
	if len(indexUpdates) > 0 {
		log.Printf("Updating %d index files...", len(indexUpdates))
		if err := a.updateIndexFiles(branchName, articles, indexUpdates); err != nil {
			slog.Warn("Failed to update some index files", "error", err)
		}
	}

	// Step 6: Move images from _incoming to their final locations
	log.Println("Moving images to final locations...")
	if err := a.moveIncomingImages(branchName, articles); err != nil {
		slog.Warn("Failed to move some images", "error", err)
	}

	// Step 7: Clean up working folders (_incoming, _debug, _config)
	log.Println("Cleaning up working folders...")
	a.cleanupWorkingFolders(branchName)

	return nil
}

// parseArticleFrontmatter extracts article info from frontmatter
func parseArticleFrontmatter(content string) (ArticleInfo, error) {
	var info ArticleInfo

	// Check for frontmatter
	if !strings.HasPrefix(content, "---") {
		return info, fmt.Errorf("no frontmatter found")
	}

	parts := strings.SplitN(content, "---", 3)
	if len(parts) < 3 {
		return info, fmt.Errorf("invalid frontmatter format")
	}

	frontmatter := parts[1]

	// Parse each field
	info.Domain = extractFrontmatterField(frontmatter, "domain")
	info.DomainSlug = extractFrontmatterField(frontmatter, "domain-slug")
	info.Category = extractFrontmatterField(frontmatter, "category")
	info.CategorySlug = extractFrontmatterField(frontmatter, "category-slug")
	info.Topic = extractFrontmatterField(frontmatter, "topic")
	info.TopicSlug = extractFrontmatterField(frontmatter, "topic-slug")
	info.Article = extractFrontmatterField(frontmatter, "article")
	info.ArticleSlug = extractFrontmatterField(frontmatter, "article-slug")
	info.GithubIssueIDs = extractGithubIssueIDs(frontmatter)

	// Validate required fields
	if info.DomainSlug == "" || info.CategorySlug == "" || info.TopicSlug == "" || info.ArticleSlug == "" {
		return info, fmt.Errorf("missing required frontmatter fields")
	}

	return info, nil
}

// extractFrontmatterField extracts a single field value from frontmatter
func extractFrontmatterField(frontmatter, field string) string {
	pattern := regexp.MustCompile(fmt.Sprintf(`(?m)^%s:\s*"?([^"\n]+)"?`, field))
	matches := pattern.FindStringSubmatch(frontmatter)
	if len(matches) >= 2 {
		return strings.TrimSpace(matches[1])
	}
	return ""
}

// updateHeaderImageReference updates header image references in article content
// Changes from ![Header](slug_header.png) to ![Header](_img/slug_header.avif)
func updateHeaderImageReference(content, articleSlug string) string {
	// Pattern to match header image references with various formats
	// Matches: ![Header](slug_header.png), ![Header](slug_header.avif), ![Header](_img/slug_header.png), etc.
	headerPattern := regexp.MustCompile(`!\[Header\]\(([^)]+)\)`)
	
	return headerPattern.ReplaceAllStringFunc(content, func(match string) string {
		// Extract the current path
		submatch := headerPattern.FindStringSubmatch(match)
		if len(submatch) < 2 {
			return match
		}
		
		currentPath := submatch[1]
		
		// If already in correct format (_img/ and .avif), keep it
		if strings.HasPrefix(currentPath, "_img/") && strings.HasSuffix(currentPath, ".avif") {
			return match
		}
		
		// Extract the base filename (without path and extension)
		baseName := filepath.Base(currentPath)
		baseName = strings.TrimSuffix(baseName, filepath.Ext(baseName))
		
		// If baseName doesn't match the article slug pattern, try to extract it
		if !strings.Contains(baseName, articleSlug) {
			// Use the article slug directly
			baseName = articleSlug + "_header"
		}
		
		// Ensure it ends with _header
		if !strings.HasSuffix(baseName, "_header") {
			baseName = baseName + "_header"
		}
		
		// Return the corrected reference
		return fmt.Sprintf("![Header](_img/%s.avif)", baseName)
	})
}

// extractGithubIssueIDs extracts the github_issue_ids array from frontmatter
func extractGithubIssueIDs(frontmatter string) []int {
	pattern := regexp.MustCompile(`github_issue_ids:\s*\[([^\]]+)\]`)
	matches := pattern.FindStringSubmatch(frontmatter)
	if len(matches) < 2 {
		return nil
	}

	var ids []int
	parts := strings.Split(matches[1], ",")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		var id int
		if _, err := fmt.Sscanf(p, "%d", &id); err == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

// updateIndexFiles creates or updates index.md files at each level
func (a *Agent) updateIndexFiles(branchName string, articles []ArticleInfo, pathsToUpdate map[string]bool) error {
	// Build a hierarchy of what exists
	domains := make(map[string]*DomainIndex)

	for _, article := range articles {
		// Ensure domain exists
		if _, ok := domains[article.DomainSlug]; !ok {
			domains[article.DomainSlug] = &DomainIndex{
				Name:       article.Domain,
				Slug:       article.DomainSlug,
				Categories: make(map[string]*CategoryIndex),
			}
		}
		domain := domains[article.DomainSlug]

		// Ensure category exists
		if _, ok := domain.Categories[article.CategorySlug]; !ok {
			domain.Categories[article.CategorySlug] = &CategoryIndex{
				Name:   article.Category,
				Slug:   article.CategorySlug,
				Topics: make(map[string]*TopicIndex),
			}
		}
		category := domain.Categories[article.CategorySlug]

		// Ensure topic exists
		if _, ok := category.Topics[article.TopicSlug]; !ok {
			category.Topics[article.TopicSlug] = &TopicIndex{
				Name:     article.Topic,
				Slug:     article.TopicSlug,
				Articles: []ArticleRef{},
			}
		}
		topic := category.Topics[article.TopicSlug]

		// Add article
		topic.Articles = append(topic.Articles, ArticleRef{
			Name: article.Article,
			Slug: article.ArticleSlug,
		})

		// Store issue IDs for hierarchy
		if len(article.GithubIssueIDs) >= 1 {
			domain.RootIssueID = article.GithubIssueIDs[0]
		}
		if len(article.GithubIssueIDs) >= 2 {
			domain.IssueID = article.GithubIssueIDs[1]
		}
		if len(article.GithubIssueIDs) >= 3 {
			category.IssueID = article.GithubIssueIDs[2]
		}
		if len(article.GithubIssueIDs) >= 4 {
			topic.IssueID = article.GithubIssueIDs[3]
		}
	}

	// Generate/update index files
	// Root index
	if pathsToUpdate["Compendium"] {
		if err := a.updateRootIndex(branchName, domains); err != nil {
			slog.Warn("Failed to update root index", "error", err)
		}
	}

	// Domain indexes
	for domainSlug, domain := range domains {
		domainPath := fmt.Sprintf("Compendium/%s", domainSlug)
		if pathsToUpdate[domainPath] {
			if err := a.updateDomainIndex(branchName, domain); err != nil {
				slog.Warn("Failed to update domain index", "domain", domainSlug, "error", err)
			}
		}

		// Category indexes
		for categorySlug, category := range domain.Categories {
			categoryPath := fmt.Sprintf("Compendium/%s/%s", domainSlug, categorySlug)
			if pathsToUpdate[categoryPath] {
				if err := a.updateCategoryIndex(branchName, domain, category); err != nil {
					slog.Warn("Failed to update category index", "category", categorySlug, "error", err)
				}
			}

			// Topic indexes
			for topicSlug, topic := range category.Topics {
				topicPath := fmt.Sprintf("Compendium/%s/%s/%s", domainSlug, categorySlug, topicSlug)
				if pathsToUpdate[topicPath] {
					if err := a.updateTopicIndex(branchName, domain, category, topic); err != nil {
						slog.Warn("Failed to update topic index", "topic", topicSlug, "error", err)
					}
				}
			}
		}
	}

	return nil
}

// Index structures
type DomainIndex struct {
	Name        string
	Slug        string
	RootIssueID int
	IssueID     int
	Categories  map[string]*CategoryIndex
}

type CategoryIndex struct {
	Name    string
	Slug    string
	IssueID int
	Topics  map[string]*TopicIndex
}

type TopicIndex struct {
	Name     string
	Slug     string
	IssueID  int
	Articles []ArticleRef
}

type ArticleRef struct {
	Name string
	Slug string
}

// updateRootIndex creates/updates Compendium/index.md
func (a *Agent) updateRootIndex(branchName string, domains map[string]*DomainIndex) error {
	// Get existing content if any
	indexPath := "Compendium/index.md"
	existingContent, sha, _ := a.gh.GetFile(branchName, indexPath)

	// Collect all domains and find root issue ID
	var domainList []string
	var rootIssueID int
	for slug, domain := range domains {
		domainList = append(domainList, slug)
		if domain.RootIssueID > 0 {
			rootIssueID = domain.RootIssueID
		}
	}
	sort.Strings(domainList)

	// Build content
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString("title: \"Encyclopedia Index\"\n")
	if rootIssueID > 0 {
		sb.WriteString(fmt.Sprintf("github_issue_ids: [%d]\n", rootIssueID))
	}
	sb.WriteString("---\n\n")
	sb.WriteString("# Encyclopedia\n\n")
	sb.WriteString("## Domains\n\n")

	for _, slug := range domainList {
		domain := domains[slug]
		sb.WriteString(fmt.Sprintf("- [%s](%s/)\n", domain.Name, slug))
	}

	content := sb.String()

	if existingContent != "" {
		return a.gh.UpdateFile(branchName, indexPath, "Update root index", content, sha)
	}
	return a.gh.CreateFile(branchName, indexPath, "Create root index", content)
}

// updateDomainIndex creates/updates Compendium/<domain>/index.md
func (a *Agent) updateDomainIndex(branchName string, domain *DomainIndex) error {
	indexPath := fmt.Sprintf("Compendium/%s/index.md", domain.Slug)
	existingContent, sha, _ := a.gh.GetFile(branchName, indexPath)

	// Check if header image exists (check both .avif and .png)
	headerImagePathBase := fmt.Sprintf("Compendium/%s/_img/%s_header", domain.Slug, domain.Slug)
	hasHeaderImage := a.imageExistsAnyFormat(branchName, headerImagePathBase)

	// Collect categories
	var categoryList []string
	for slug := range domain.Categories {
		categoryList = append(categoryList, slug)
	}
	sort.Strings(categoryList)

	// Build content
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("title: \"%s\"\n", domain.Name))
	sb.WriteString(fmt.Sprintf("domain: \"%s\"\n", domain.Name))
	sb.WriteString(fmt.Sprintf("domain-slug: \"%s\"\n", domain.Slug))
	if domain.RootIssueID > 0 && domain.IssueID > 0 {
		sb.WriteString(fmt.Sprintf("github_issue_ids: [%d, %d]\n", domain.RootIssueID, domain.IssueID))
	}
	sb.WriteString("---\n\n")

	// Add header image if it exists (always use .avif extension for published content)
	if hasHeaderImage {
		sb.WriteString(fmt.Sprintf("![Header](_img/%s_header.avif)\n\n", domain.Slug))
	}

	sb.WriteString(fmt.Sprintf("# %s\n\n", domain.Name))
	sb.WriteString("## Categories\n\n")

	for _, slug := range categoryList {
		category := domain.Categories[slug]
		sb.WriteString(fmt.Sprintf("- [%s](%s/)\n", category.Name, slug))
	}

	content := sb.String()

	if existingContent != "" {
		return a.gh.UpdateFile(branchName, indexPath, fmt.Sprintf("Update %s index", domain.Name), content, sha)
	}
	return a.gh.CreateFile(branchName, indexPath, fmt.Sprintf("Create %s index", domain.Name), content)
}

// updateCategoryIndex creates/updates Compendium/<domain>/<category>/index.md
func (a *Agent) updateCategoryIndex(branchName string, domain *DomainIndex, category *CategoryIndex) error {
	indexPath := fmt.Sprintf("Compendium/%s/%s/index.md", domain.Slug, category.Slug)
	existingContent, sha, _ := a.gh.GetFile(branchName, indexPath)

	// Check if header image exists (check both .avif and .png)
	headerImagePathBase := fmt.Sprintf("Compendium/%s/%s/_img/%s_header", domain.Slug, category.Slug, category.Slug)
	hasHeaderImage := a.imageExistsAnyFormat(branchName, headerImagePathBase)

	// Collect topics
	var topicList []string
	for slug := range category.Topics {
		topicList = append(topicList, slug)
	}
	sort.Strings(topicList)

	// Build content
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("title: \"%s\"\n", category.Name))
	sb.WriteString(fmt.Sprintf("domain: \"%s\"\n", domain.Name))
	sb.WriteString(fmt.Sprintf("domain-slug: \"%s\"\n", domain.Slug))
	sb.WriteString(fmt.Sprintf("category: \"%s\"\n", category.Name))
	sb.WriteString(fmt.Sprintf("category-slug: \"%s\"\n", category.Slug))
	if domain.RootIssueID > 0 && domain.IssueID > 0 && category.IssueID > 0 {
		sb.WriteString(fmt.Sprintf("github_issue_ids: [%d, %d, %d]\n", domain.RootIssueID, domain.IssueID, category.IssueID))
	}
	sb.WriteString("---\n\n")

	// Add header image if it exists (always use .avif extension for published content)
	if hasHeaderImage {
		sb.WriteString(fmt.Sprintf("![Header](_img/%s_header.avif)\n\n", category.Slug))
	}

	sb.WriteString(fmt.Sprintf("# %s\n\n", category.Name))
	sb.WriteString("## Topics\n\n")

	for _, slug := range topicList {
		topic := category.Topics[slug]
		sb.WriteString(fmt.Sprintf("- [%s](%s/)\n", topic.Name, slug))
	}

	content := sb.String()

	if existingContent != "" {
		return a.gh.UpdateFile(branchName, indexPath, fmt.Sprintf("Update %s index", category.Name), content, sha)
	}
	return a.gh.CreateFile(branchName, indexPath, fmt.Sprintf("Create %s index", category.Name), content)
}

// updateTopicIndex creates/updates Compendium/<domain>/<category>/<topic>/index.md
func (a *Agent) updateTopicIndex(branchName string, domain *DomainIndex, category *CategoryIndex, topic *TopicIndex) error {
	indexPath := fmt.Sprintf("Compendium/%s/%s/%s/index.md", domain.Slug, category.Slug, topic.Slug)
	existingContent, sha, _ := a.gh.GetFile(branchName, indexPath)

	// Check if header image exists (check both .avif and .png)
	headerImagePathBase := fmt.Sprintf("Compendium/%s/%s/%s/_img/%s_header", domain.Slug, category.Slug, topic.Slug, topic.Slug)
	hasHeaderImage := a.imageExistsAnyFormat(branchName, headerImagePathBase)

	// Sort articles
	sort.Slice(topic.Articles, func(i, j int) bool {
		return topic.Articles[i].Name < topic.Articles[j].Name
	})

	// Build content
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("title: \"%s\"\n", topic.Name))
	sb.WriteString(fmt.Sprintf("domain: \"%s\"\n", domain.Name))
	sb.WriteString(fmt.Sprintf("domain-slug: \"%s\"\n", domain.Slug))
	sb.WriteString(fmt.Sprintf("category: \"%s\"\n", category.Name))
	sb.WriteString(fmt.Sprintf("category-slug: \"%s\"\n", category.Slug))
	sb.WriteString(fmt.Sprintf("topic: \"%s\"\n", topic.Name))
	sb.WriteString(fmt.Sprintf("topic-slug: \"%s\"\n", topic.Slug))
	if domain.RootIssueID > 0 && domain.IssueID > 0 && category.IssueID > 0 && topic.IssueID > 0 {
		sb.WriteString(fmt.Sprintf("github_issue_ids: [%d, %d, %d, %d]\n", domain.RootIssueID, domain.IssueID, category.IssueID, topic.IssueID))
	}
	sb.WriteString("---\n\n")

	// Add header image if it exists (always use .avif extension for published content)
	if hasHeaderImage {
		sb.WriteString(fmt.Sprintf("![Header](_img/%s_header.avif)\n\n", topic.Slug))
	}

	sb.WriteString(fmt.Sprintf("# %s\n\n", topic.Name))
	sb.WriteString("## Articles\n\n")

	for _, article := range topic.Articles {
		sb.WriteString(fmt.Sprintf("- [%s](%s.md)\n", article.Name, article.Slug))
	}

	content := sb.String()

	if existingContent != "" {
		return a.gh.UpdateFile(branchName, indexPath, fmt.Sprintf("Update %s index", topic.Name), content, sha)
	}
	return a.gh.CreateFile(branchName, indexPath, fmt.Sprintf("Create %s index", topic.Name), content)
}

// moveIncomingImages moves images from _incoming to their final locations
func (a *Agent) moveIncomingImages(branchName string, articles []ArticleInfo) error {
	var moveErrors []error

	// Move article header images
	for _, article := range articles {
		articleSlug := article.ArticleSlug
		targetDir := fmt.Sprintf("Compendium/%s/%s/%s",
			article.DomainSlug, article.CategorySlug, article.TopicSlug)

		// Move header image and its medium variant
		for _, suffix := range []string{"_header.png", "_header-medium.png"} {
			srcPath := fmt.Sprintf("Compendium/_incoming/%s%s", articleSlug, suffix)
			dstPath := fmt.Sprintf("%s/%s%s", targetDir, articleSlug, suffix)

			content, sha, err := a.gh.GetFile(branchName, srcPath)
			if err != nil {
				continue // Image doesn't exist, skip
			}

			// Create at destination (as binary)
			if err := a.gh.CreateFile(branchName, dstPath,
				fmt.Sprintf("Move image: %s", filepath.Base(srcPath)), content); err != nil {
				slog.Warn("Failed to create image at destination", "src", srcPath, "dst", dstPath, "error", err)
				moveErrors = append(moveErrors, err)
				continue
			}

			// Delete from source
			if err := a.gh.DeleteFile(branchName, srcPath,
				fmt.Sprintf("Remove from _incoming: %s", filepath.Base(srcPath)), sha); err != nil {
				slog.Warn("Failed to delete source image", "path", srcPath, "error", err)
			}
		}
	}

	// Move index images (domains, categories, topics)
	indexFolders := []struct {
		srcFolder string
		parseFunc func(filename string) (dstPath string, ok bool)
	}{
		{
			srcFolder: "Compendium/_incoming/indexes/domains",
			parseFunc: func(filename string) (string, bool) {
				// Format: <domain>_header.png or <domain>_header-medium.png
				slug := strings.TrimSuffix(strings.TrimSuffix(filename, ".png"), "_header")
				slug = strings.TrimSuffix(slug, "-medium")
				isMedium := strings.Contains(filename, "-medium")
				suffix := "_header.png"
				if isMedium {
					suffix = "_header-medium.png"
				}
				return fmt.Sprintf("Compendium/%s/_img/%s%s", slug, slug, suffix), true
			},
		},
		{
			srcFolder: "Compendium/_incoming/indexes/categories",
			parseFunc: func(filename string) (string, bool) {
				// Format: <domain>--<category>_header.png or <domain>--<category>_header-medium.png
				base := strings.TrimSuffix(strings.TrimSuffix(filename, ".png"), "_header")
				base = strings.TrimSuffix(base, "-medium")
				parts := strings.SplitN(base, "--", 2)
				if len(parts) != 2 {
					return "", false
				}
				isMedium := strings.Contains(filename, "-medium")
				suffix := "_header.png"
				if isMedium {
					suffix = "_header-medium.png"
				}
				return fmt.Sprintf("Compendium/%s/%s/_img/%s%s", parts[0], parts[1], parts[1], suffix), true
			},
		},
		{
			srcFolder: "Compendium/_incoming/indexes/topics",
			parseFunc: func(filename string) (string, bool) {
				// Format: <domain>--<category>--<topic>_header.png or -medium variant
				base := strings.TrimSuffix(strings.TrimSuffix(filename, ".png"), "_header")
				base = strings.TrimSuffix(base, "-medium")
				parts := strings.SplitN(base, "--", 3)
				if len(parts) != 3 {
					return "", false
				}
				isMedium := strings.Contains(filename, "-medium")
				suffix := "_header.png"
				if isMedium {
					suffix = "_header-medium.png"
				}
				return fmt.Sprintf("Compendium/%s/%s/%s/_img/%s%s", parts[0], parts[1], parts[2], parts[2], suffix), true
			},
		},
	}

	for _, folder := range indexFolders {
		files, err := a.gh.ListFilesInBranch(branchName, folder.srcFolder)
		if err != nil {
			continue // Folder might not exist
		}

		for _, file := range files {
			filename := filepath.Base(file)
			dstPath, ok := folder.parseFunc(filename)
			if !ok {
				slog.Warn("Failed to parse index image filename", "file", file)
				continue
			}

			content, sha, err := a.gh.GetFile(branchName, file)
			if err != nil {
				continue
			}

			// Create at destination
			if err := a.gh.CreateFile(branchName, dstPath,
				fmt.Sprintf("Move index image: %s", filename), content); err != nil {
				slog.Warn("Failed to create index image at destination", "src", file, "dst", dstPath, "error", err)
				moveErrors = append(moveErrors, err)
				continue
			}

			// Delete from source
			if err := a.gh.DeleteFile(branchName, file,
				fmt.Sprintf("Remove from _incoming: %s", filename), sha); err != nil {
				slog.Warn("Failed to delete source index image", "path", file, "error", err)
			}

			log.Printf("Moved index image: %s -> %s", file, dstPath)
		}
	}

	if len(moveErrors) > 0 {
		return fmt.Errorf("failed to move %d images", len(moveErrors))
	}
	return nil
}

// cleanupWorkingFolders removes _incoming, _debug, and _config folders from Compendium
func (a *Agent) cleanupWorkingFolders(branchName string) {
	// Folders to completely remove (all at Compendium/ root level)
	foldersToClean := []string{
		"Compendium/_incoming",
		"Compendium/_debug",
		"Compendium/_config",
	}

	for _, folder := range foldersToClean {
		files, err := a.gh.ListFilesInBranch(branchName, folder)
		if err != nil {
			continue // Folder might not exist
		}

		log.Printf("Cleaning up %s (%d files)...", folder, len(files))
		for _, file := range files {
			_, sha, err := a.gh.GetFile(branchName, file)
			if err != nil {
				continue
			}
			if err := a.gh.DeleteFile(branchName, file, "Cleanup: remove "+file, sha); err != nil {
				slog.Warn("Failed to delete file during cleanup", "file", file, "error", err)
			}
		}

		// Remove empty directories locally if in local mode
		if a.gh.IsLocal() {
			a.removeEmptyDirs(folder)
		}
	}
}

// removeEmptyDirs removes a directory and all empty parent directories
func (a *Agent) removeEmptyDirs(path string) {
	repoMgr, ok := a.gh.(interface{ GetRepoPath() string })
	if !ok {
		return
	}
	repoPath := repoMgr.GetRepoPath()
	fullPath := filepath.Join(repoPath, path)

	// Walk the directory tree bottom-up and remove empty directories
	filepath.Walk(fullPath, func(p string, info os.FileInfo, err error) error {
		return nil // Just traverse, we'll handle deletion after
	})

	// Remove the directory tree (will only remove if empty)
	removeEmptyDirTree(fullPath)
}

// removeEmptyDirTree removes empty directories bottom-up
func removeEmptyDirTree(path string) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return
	}

	// First, recursively clean subdirectories
	for _, entry := range entries {
		if entry.IsDir() {
			removeEmptyDirTree(filepath.Join(path, entry.Name()))
		}
	}

	// Then try to remove this directory (will fail if not empty, which is fine)
	os.Remove(path)
}
