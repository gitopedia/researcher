package agent

import (
	"fmt"
	"log"
	"log/slog"
	"regexp"
	"strings"
)

// normalizeMarkdown cleans up markdown formatting inconsistencies:
// - Normalizes multiple blank lines before headers to a single blank line
// - Ensures consistent spacing throughout the document
// - Preserves frontmatter formatting
func normalizeMarkdown(content string) string {
	lines := strings.Split(content, "\n")
	var result []string

	inFrontmatter := false
	frontmatterCount := 0

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// Track frontmatter boundaries
		if trimmed == "---" {
			frontmatterCount++
			if frontmatterCount == 1 {
				inFrontmatter = true
			} else if frontmatterCount == 2 {
				inFrontmatter = false
			}
			result = append(result, line)
			continue
		}

		// Preserve frontmatter as-is
		if inFrontmatter {
			result = append(result, line)
			continue
		}

		// Check if current line is a heading
		isHeading := strings.HasPrefix(trimmed, "#")

		// If this is a heading, ensure exactly one blank line before it
		// (unless it's at the start of content after frontmatter)
		if isHeading && len(result) > 0 {
			// Remove trailing blank lines from result
			for len(result) > 0 && strings.TrimSpace(result[len(result)-1]) == "" {
				result = result[:len(result)-1]
			}
			// Add exactly one blank line before heading
			result = append(result, "")
			result = append(result, line)
			continue
		}

		// For non-heading lines, collapse multiple consecutive blank lines to one
		if trimmed == "" {
			// Check if last line in result is already blank
			if len(result) > 0 && strings.TrimSpace(result[len(result)-1]) == "" {
				// Skip this blank line (would create multiple consecutive blanks)
				continue
			}
		}

		result = append(result, line)
	}

	// Trim trailing blank lines
	for len(result) > 0 && strings.TrimSpace(result[len(result)-1]) == "" {
		result = result[:len(result)-1]
	}

	return strings.Join(result, "\n")
}

// normalizeReferencesSection ensures the References section is properly formatted
func normalizeReferencesSection(content string) string {
	// Regex to find References section and normalize its formatting
	// This ensures each reference is on its own line with no extra blank lines
	refSectionRegex := regexp.MustCompile(`(?m)(## References\n)(\n*)((?:\[\^[^\]]+\]:.*\n?)+)`)

	return refSectionRegex.ReplaceAllStringFunc(content, func(match string) string {
		// Extract the references
		parts := refSectionRegex.FindStringSubmatch(match)
		if len(parts) < 4 {
			return match
		}

		header := parts[1]
		refs := strings.TrimSpace(parts[3])

		// Split references and rejoin with single newlines
		refLines := strings.Split(refs, "\n")
		var cleanRefs []string
		for _, line := range refLines {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" {
				cleanRefs = append(cleanRefs, trimmed)
			}
		}

		return header + "\n" + strings.Join(cleanRefs, "\n")
	})
}

// removeEmptySections removes sections that have a heading but no content
// (i.e., heading followed immediately by another heading or end of file)
func removeEmptySections(content string) string {
	lines := strings.Split(content, "\n")
	var result []string

	inFrontmatter := false
	frontmatterCount := 0

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// Track frontmatter boundaries
		if trimmed == "---" {
			frontmatterCount++
			if frontmatterCount == 1 {
				inFrontmatter = true
			} else if frontmatterCount == 2 {
				inFrontmatter = false
			}
			result = append(result, line)
			continue
		}

		// Preserve frontmatter as-is
		if inFrontmatter {
			result = append(result, line)
			continue
		}

		// Check if current line is a heading
		if strings.HasPrefix(trimmed, "#") {
			// Look ahead to see if this section has any content
			hasContent := false
			for j := i + 1; j < len(lines); j++ {
				nextTrimmed := strings.TrimSpace(lines[j])
				// Skip blank lines
				if nextTrimmed == "" {
					continue
				}
				// If we hit another heading, this section is empty
				if strings.HasPrefix(nextTrimmed, "#") {
					break
				}
				// Found content
				hasContent = true
				break
			}

			// Skip empty sections (but always keep References even if empty)
			if !hasContent && !strings.Contains(strings.ToLower(trimmed), "references") {
				log.Printf("[Normalize] Removing empty section: %s", trimmed)
				continue
			}
		}

		result = append(result, line)
	}

	return strings.Join(result, "\n")
}

// removeFAQSections removes FAQ/Q&A style sections from the article
func removeFAQSections(content string) string {
	lines := strings.Split(content, "\n")
	var result []string

	inFAQSection := false
	faqSectionLevel := 0

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Check if this is a heading
		if strings.HasPrefix(trimmed, "#") {
			headingLevel := 0
			for _, c := range trimmed {
				if c == '#' {
					headingLevel++
				} else {
					break
				}
			}
			headingTitle := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
			lowerTitle := strings.ToLower(headingTitle)

			// Check if this is an FAQ-style section
			isFAQ := strings.Contains(lowerTitle, "faq") ||
				strings.Contains(lowerTitle, "frequently asked") ||
				strings.Contains(lowerTitle, "questions and answers") ||
				strings.Contains(lowerTitle, "q&a") ||
				strings.Contains(lowerTitle, "common questions")

			if isFAQ {
				log.Printf("[Normalize] Removing FAQ section: %s", headingTitle)
				inFAQSection = true
				faqSectionLevel = headingLevel
				continue
			}

			// If we hit a heading of same or higher level, exit FAQ section
			if inFAQSection && headingLevel <= faqSectionLevel {
				inFAQSection = false
			}
		}

		// Skip content in FAQ sections
		if inFAQSection {
			continue
		}

		result = append(result, line)
	}

	return strings.Join(result, "\n")
}

// removeDuplicateTitleHeadings removes headings that duplicate the article title
// (e.g., a "# Quantum Zeno Effect" at the end when the title is already in frontmatter)
func removeDuplicateTitleHeadings(content string) string {
	lines := strings.Split(content, "\n")

	// Extract title from frontmatter
	var articleTitle string
	inFrontmatter := false
	frontmatterCount := 0

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			frontmatterCount++
			if frontmatterCount == 1 {
				inFrontmatter = true
			} else if frontmatterCount == 2 {
				break
			}
			continue
		}
		if inFrontmatter && strings.HasPrefix(trimmed, "title:") {
			articleTitle = strings.TrimSpace(strings.TrimPrefix(trimmed, "title:"))
			articleTitle = strings.Trim(articleTitle, "\"'")
			break
		}
	}

	if articleTitle == "" {
		return content
	}

	// Now filter out duplicate title headings
	var result []string
	frontmatterCount = 0
	inFrontmatter = false
	titleLower := strings.ToLower(articleTitle)

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Track frontmatter
		if trimmed == "---" {
			frontmatterCount++
			if frontmatterCount == 1 {
				inFrontmatter = true
			} else if frontmatterCount == 2 {
				inFrontmatter = false
			}
			result = append(result, line)
			continue
		}

		if inFrontmatter {
			result = append(result, line)
			continue
		}

		// Check if this is a heading that matches the title
		if strings.HasPrefix(trimmed, "#") {
			headingTitle := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
			if strings.ToLower(headingTitle) == titleLower {
				log.Printf("[Normalize] Removing duplicate title heading: %s", headingTitle)
				continue
			}
		}

		result = append(result, line)
	}

	return strings.Join(result, "\n")
}

// NormalizeAllArticles normalizes all markdown files in the _incoming folder on the given branch
func (a *Agent) NormalizeAllArticles(branchName string) error {
	log.Println("=== Running Markdown Normalization ===")

	incomingPath := "Compendium/_incoming"
	files, err := a.gh.ListDirectory(branchName, incomingPath)
	if err != nil {
		return fmt.Errorf("failed to list incoming articles: %w", err)
	}

	normalizedCount := 0
	for _, file := range files {
		// Only process markdown files
		if !strings.HasSuffix(file, ".md") {
			continue
		}

		articlePath := fmt.Sprintf("%s/%s", incomingPath, file)
		content, sha, err := a.gh.GetFile(branchName, articlePath)
		if err != nil {
			slog.Warn("Failed to read article for normalization", "file", file, "error", err)
			continue
		}

		// Apply normalization pipeline
		normalized := removeFAQSections(content)
		normalized = removeEmptySections(normalized)
		normalized = removeDuplicateTitleHeadings(normalized)
		normalized = normalizeMarkdown(normalized)
		normalized = normalizeReferencesSection(normalized)

		// Only update if content changed
		if normalized != content {
			if err := a.gh.UpdateFile(branchName, articlePath, fmt.Sprintf("Normalize formatting: %s", file), normalized, sha); err != nil {
				slog.Warn("Failed to save normalized article", "file", file, "error", err)
				continue
			}
			normalizedCount++
			log.Printf("[Normalize] Fixed formatting in %s", file)
		}
	}

	log.Printf("[Normalize] Completed: %d files normalized", normalizedCount)
	return nil
}
