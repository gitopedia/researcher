package authority

import "testing"

func TestSanitizeSlug(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple name",
			input:    "Albert Einstein",
			expected: "albert-einstein",
		},
		{
			name:     "name with apostrophe",
			input:    "Cathy O'Neil",
			expected: "cathy-oneil",
		},
		{
			name:     "name with parenthetical qualifier",
			input:    "John Smith (physicist)",
			expected: "john-smith",
		},
		{
			name:     "complex name with quotes and parentheses",
			input:    `Cathy O'Neil (author, "Weapons of Math Destruction")`,
			expected: "cathy-oneil",
		},
		{
			name:     "name with nested quotes",
			input:    `Joy Buolamwini (researcher, "Gender Shades" study)`,
			expected: "joy-buolamwini",
		},
		{
			name:     "organization with period",
			input:    "Apple Inc.",
			expected: "apple-inc",
		},
		{
			name:     "place with comma",
			input:    "Cambridge, Massachusetts",
			expected: "cambridge-massachusetts",
		},
		{
			name:     "topic with special chars",
			input:    "AI & Machine Learning",
			expected: "ai-machine-learning",
		},
		{
			name:     "multiple spaces",
			input:    "United   Nations",
			expected: "united-nations",
		},
		{
			name:     "brackets and colons",
			input:    "Topic: [Advanced] Concepts",
			expected: "topic-advanced-concepts",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeSlug(tt.input)
			if result != tt.expected {
				t.Errorf("sanitizeSlug(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

