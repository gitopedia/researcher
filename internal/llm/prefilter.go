package llm

import (
	"regexp"
	"strings"
)

// PreFilterContent removes navigation, TOC, and junk from raw web content using regex.
// This is designed to be SITE-AGNOSTIC - patterns work across many websites.
// Subheadings and topic names are intentionally preserved as valuable content.
func PreFilterContent(content string) string {
	lines := strings.Split(content, "\n")
	var cleanLines []string

	// GENERIC patterns to skip (work across many sites)
	// NOTE: We keep subheadings and topic names - those are valuable content!
	skipPatterns := []*regexp.Regexp{
		// Interactive TOC elements (contain "Toggle" for expand/collapse)
		regexp.MustCompile(`(?i)\bToggle\b.*(subsection|section|menu)`),

		// Navigation commands
		regexp.MustCompile(`(?i)^(Jump to|Skip to|Go to|Back to)\s`),
		regexp.MustCompile(`(?i)^Edit\s+(interlanguage|links|this|page)`),

		// CSS/HTML artifacts embedded in text
		regexp.MustCompile(`\.mw-parser-output`),    // Wikipedia CSS classes
		regexp.MustCompile(`\{display:\s*none\}`),   // CSS rules
		regexp.MustCompile(`^\.[a-z]+-[a-z]+\s*\{`), // CSS class definitions

		// Cookie/privacy banners
		regexp.MustCompile(`(?i)^(Accept|Reject|Manage)\s+(all\s+)?(cookies?|preferences)`),

		// Social sharing (standalone buttons)
		regexp.MustCompile(`(?i)^(Share|Tweet|Pin)(\s+(this|on|to))?\s*$`),

		// Footer copyright
		regexp.MustCompile(`(?i)^(All rights reserved|©\s*\d{4})`),

		// Standalone citation markers (not inline citations)
		regexp.MustCompile(`^\s*\[\d+\]\s*$`), // Just "[1]"
		regexp.MustCompile(`^\s*\^\s*$`),      // Just "^"

		// Language selector lines (e.g., "Српски / srpski")
		regexp.MustCompile(`^[\p{Cyrillic}\p{Arabic}\p{Han}]+\s*/\s*[a-z]+$`),
	}

	// GENERIC junk indicators (phrases found in web cruft across many sites)
	junkIndicators := []string{
		// Cookie/consent
		"cookie policy", "cookie settings", "accept cookies", "we use cookies",
		"privacy policy", "terms of use", "terms of service", "terms and conditions",

		// Copyright
		"all rights reserved", "registered trademark", "creative commons",
		"this page was last edited", "text is available under",

		// Newsletter/subscription
		"subscribe to our", "sign up for", "join our mailing", "enter your email",

		// Navigation
		"toggle menu", "toggle navigation", "show more", "load more", "read more...",
		"toggle subsection", "toggle section",

		// Ads
		"advertisement", "sponsored content", "promoted",

		// Social
		"follow us on", "share this article", "share on facebook", "share on twitter",

		// Promo banners
		"participate in the", "photo competition", "enter to win",
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Skip empty lines (but preserve paragraph breaks)
		if trimmed == "" {
			if len(cleanLines) > 0 && cleanLines[len(cleanLines)-1] != "" {
				cleanLines = append(cleanLines, "")
			}
			continue
		}

		// Skip very short lines (< 5 chars, like stray punctuation)
		if len(trimmed) < 5 {
			continue
		}

		// Check against skip patterns
		skip := false
		for _, pattern := range skipPatterns {
			if pattern.MatchString(trimmed) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}

		// Check for junk indicator phrases
		lowerLine := strings.ToLower(trimmed)
		for _, indicator := range junkIndicators {
			if strings.Contains(lowerLine, indicator) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}

		// Keep the line
		cleanLines = append(cleanLines, trimmed)
	}

	// Join and clean up multiple empty lines
	result := strings.Join(cleanLines, "\n")
	result = regexp.MustCompile(`\n{3,}`).ReplaceAllString(result, "\n\n")
	return strings.TrimSpace(result)
}

// FormatContent converts filtered paragraphs to bullet points with a heading.
func FormatContent(content string, topic string) string {
	var sb strings.Builder
	sb.WriteString("# " + topic + "\n\n")

	paragraphs := strings.Split(content, "\n\n")
	for _, para := range paragraphs {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}

		// Check if it's a reference/citation line
		if strings.HasPrefix(para, "^") || strings.HasPrefix(para, "[") {
			continue
		}

		// If paragraph is short enough, keep it as one bullet
		if len(para) < 300 {
			sb.WriteString("- " + para + "\n")
		} else {
			// Split long paragraphs into sentences and format as bullets
			sentences := splitIntoSentences(para)
			for _, sent := range sentences {
				sent = strings.TrimSpace(sent)
				if len(sent) > 30 { // Only meaningful sentences
					sb.WriteString("- " + sent + "\n")
				}
			}
		}
		sb.WriteString("\n")
	}

	return strings.TrimSpace(sb.String())
}

// splitIntoSentences splits text into sentences
func splitIntoSentences(text string) []string {
	var sentences []string
	current := ""

	for i, ch := range text {
		current += string(ch)

		if (ch == '.' || ch == '?' || ch == '!') && i < len(text)-2 {
			next := text[i+1]
			nextNext := text[i+2]
			// Check if followed by space and capital letter
			if next == ' ' && nextNext >= 'A' && nextNext <= 'Z' {
				sentences = append(sentences, strings.TrimSpace(current))
				current = ""
			}
		}
	}

	// Add remaining text
	if strings.TrimSpace(current) != "" {
		sentences = append(sentences, strings.TrimSpace(current))
	}

	return sentences
}

// ExtractTopicsFromContent extracts topic headings from formatted content
func ExtractTopicsFromContent(content string) []string {
	var topics []string
	lines := strings.Split(content, "\n")
	
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// Look for heading lines starting with #
		if strings.HasPrefix(line, "# ") {
			topic := strings.TrimPrefix(line, "# ")
			if len(topic) > 0 && len(topic) < 100 {
				topics = append(topics, topic)
			}
		}
	}
	
	// If no headings found, extract from content structure
	if len(topics) == 0 {
		// Look for short title-like lines (subheadings preserved from source)
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "- ") {
				bullet := strings.TrimPrefix(line, "- ")
				// Short lines without periods are likely subheadings
				if len(bullet) < 60 && !strings.Contains(bullet, ".") && !strings.Contains(bullet, ",") {
					topics = append(topics, bullet)
					if len(topics) >= 15 { // Limit to 15 topics
						break
					}
				}
			}
		}
	}
	
	// Default if nothing found
	if len(topics) == 0 {
		topics = []string{"Overview"}
	}
	
	return topics
}




