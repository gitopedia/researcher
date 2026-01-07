// Package styles provides configuration loading and resolution for image generation styles and prompt templates.
package styles

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// StyleConfig represents the artistic_styles.yaml configuration
type StyleConfig struct {
	Default    map[string][]string `yaml:"default"`
	Categories map[string]struct {
		Header        []string `yaml:"header,omitempty"`
		Diagram       []string `yaml:"diagram,omitempty"`
		Subcategories map[string]struct {
			Header  []string `yaml:"header,omitempty"`
			Diagram []string `yaml:"diagram,omitempty"`
		} `yaml:"subcategories,omitempty"`
	} `yaml:"categories"`
}

// PromptTemplateConfig represents the image_prompt_templates.yaml configuration
type PromptTemplateConfig struct {
	Default    map[string]PromptTemplate `yaml:"default"`
	Categories map[string]struct {
		Header        PromptTemplate `yaml:"header,omitempty"`
		Diagram       PromptTemplate `yaml:"diagram,omitempty"`
		Subcategories map[string]struct {
			Header  PromptTemplate `yaml:"header,omitempty"`
			Diagram PromptTemplate `yaml:"diagram,omitempty"`
		} `yaml:"subcategories,omitempty"`
	} `yaml:"categories"`
}

// PromptTemplate contains all the configuration for generating an image prompt
type PromptTemplate struct {
	Template          string   `yaml:"template,omitempty"`
	Guidance          string   `yaml:"guidance,omitempty"`
	SuggestedElements []string `yaml:"suggested_elements,omitempty"`
	ColorMoods        []string `yaml:"color_moods,omitempty"`
}

// ResolvedPromptConfig contains all resolved configuration for generating an image prompt
type ResolvedPromptConfig struct {
	Template          string
	Guidance          string
	SuggestedElements []string
	ColorMoods        []string
	ArtisticStyles    []string
}

// Manager handles loading and resolving style and prompt template configurations
type Manager struct {
	styleConfig  *StyleConfig
	promptConfig *PromptTemplateConfig
	configDir    string
	rng          *rand.Rand
}

// NewManager creates a new style manager that loads configs from the given directory
func NewManager(configDir string) *Manager {
	return &Manager{
		configDir: configDir,
		rng:       rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// Load loads both configuration files from the config directory
func (m *Manager) Load() error {
	// Load artistic styles
	stylesPath := filepath.Join(m.configDir, "artistic_styles.yaml")
	stylesData, err := os.ReadFile(stylesPath)
	if err != nil {
		return fmt.Errorf("failed to read artistic_styles.yaml: %w", err)
	}

	m.styleConfig = &StyleConfig{}
	if err := yaml.Unmarshal(stylesData, m.styleConfig); err != nil {
		return fmt.Errorf("failed to parse artistic_styles.yaml: %w", err)
	}

	// Load prompt templates
	promptsPath := filepath.Join(m.configDir, "image_prompt_templates.yaml")
	promptsData, err := os.ReadFile(promptsPath)
	if err != nil {
		return fmt.Errorf("failed to read image_prompt_templates.yaml: %w", err)
	}

	m.promptConfig = &PromptTemplateConfig{}
	if err := yaml.Unmarshal(promptsData, m.promptConfig); err != nil {
		return fmt.Errorf("failed to parse image_prompt_templates.yaml: %w", err)
	}

	return nil
}

// ResolveStyles resolves the available artistic styles for the given image type, category, and subcategory
// Resolution order:
// 1. categories.<Category>.subcategories.<Subcategory>.<imageType>
// 2. categories.<Category>.<imageType>
// 3. default.<imageType>
func (m *Manager) ResolveStyles(imageType, category, subcategory string) []string {
	if m.styleConfig == nil {
		return []string{"modern illustration"}
	}

	// Normalize inputs
	category = strings.TrimSpace(category)
	subcategory = strings.TrimSpace(subcategory)
	imageType = strings.ToLower(strings.TrimSpace(imageType))

	// Try subcategory-specific styles first
	if category != "" && subcategory != "" {
		if cat, ok := m.styleConfig.Categories[category]; ok {
			if subcat, ok := cat.Subcategories[subcategory]; ok {
				styles := m.getStylesByType(subcat.Header, subcat.Diagram, imageType)
				if len(styles) > 0 {
					return styles
				}
			}
		}
	}

	// Fall back to category-level styles
	if category != "" {
		if cat, ok := m.styleConfig.Categories[category]; ok {
			styles := m.getStylesByType(cat.Header, cat.Diagram, imageType)
			if len(styles) > 0 {
				return styles
			}
		}
	}

	// Fall back to default styles
	if styles, ok := m.styleConfig.Default[imageType]; ok && len(styles) > 0 {
		return styles
	}

	// Ultimate fallback
	return []string{"modern illustration", "clean design"}
}

// getStylesByType returns the appropriate styles slice based on image type
func (m *Manager) getStylesByType(header, diagram []string, imageType string) []string {
	switch imageType {
	case "header":
		return header
	case "diagram":
		return diagram
	default:
		return header // Default to header styles
	}
}

// ResolvePromptTemplate resolves the prompt template configuration for the given parameters
// Resolution follows the same hierarchy as styles, with merging of suggested_elements
func (m *Manager) ResolvePromptTemplate(imageType, category, subcategory string) PromptTemplate {
	if m.promptConfig == nil {
		return PromptTemplate{
			Template: "An illustration depicting {{.Topic}} in a modern style.",
			Guidance: "Create a visually appealing image.",
		}
	}

	// Normalize inputs
	category = strings.TrimSpace(category)
	subcategory = strings.TrimSpace(subcategory)
	imageType = strings.ToLower(strings.TrimSpace(imageType))

	var result PromptTemplate

	// Start with defaults
	if defTemplate, ok := m.promptConfig.Default[imageType]; ok {
		result = m.mergeTemplates(result, defTemplate)
	}

	// Merge category-level config
	if category != "" {
		if cat, ok := m.promptConfig.Categories[category]; ok {
			catTemplate := m.getTemplateByType(cat.Header, cat.Diagram, imageType)
			result = m.mergeTemplates(result, catTemplate)

			// Merge subcategory-level config
			if subcategory != "" {
				if subcat, ok := cat.Subcategories[subcategory]; ok {
					subcatTemplate := m.getTemplateByType(subcat.Header, subcat.Diagram, imageType)
					result = m.mergeTemplates(result, subcatTemplate)
				}
			}
		}
	}

	return result
}

// getTemplateByType returns the appropriate template based on image type
func (m *Manager) getTemplateByType(header, diagram PromptTemplate, imageType string) PromptTemplate {
	switch imageType {
	case "header":
		return header
	case "diagram":
		return diagram
	default:
		return header
	}
}

// mergeTemplates merges two templates, with 'override' taking precedence for non-empty fields
// SuggestedElements are combined (not replaced)
func (m *Manager) mergeTemplates(base, override PromptTemplate) PromptTemplate {
	result := base

	if override.Template != "" {
		result.Template = override.Template
	}
	if override.Guidance != "" {
		result.Guidance = override.Guidance
	}
	if len(override.ColorMoods) > 0 {
		result.ColorMoods = override.ColorMoods
	}

	// Combine suggested elements (subcategory elements are more specific)
	if len(override.SuggestedElements) > 0 {
		// Prepend override elements (more specific) to base elements
		combined := make([]string, 0, len(override.SuggestedElements)+len(base.SuggestedElements))
		combined = append(combined, override.SuggestedElements...)
		combined = append(combined, base.SuggestedElements...)
		result.SuggestedElements = combined
	}

	return result
}

// ResolveAll resolves both styles and prompt template, returning a complete configuration
func (m *Manager) ResolveAll(imageType, category, subcategory string) ResolvedPromptConfig {
	template := m.ResolvePromptTemplate(imageType, category, subcategory)
	styles := m.ResolveStyles(imageType, category, subcategory)

	return ResolvedPromptConfig{
		Template:          template.Template,
		Guidance:          template.Guidance,
		SuggestedElements: template.SuggestedElements,
		ColorMoods:        template.ColorMoods,
		ArtisticStyles:    styles,
	}
}

// SelectRandomStyles randomly selects n styles from the available styles
func (m *Manager) SelectRandomStyles(styles []string, n int) []string {
	if len(styles) == 0 {
		return []string{}
	}
	if n >= len(styles) {
		return styles
	}

	// Shuffle and take first n
	shuffled := make([]string, len(styles))
	copy(shuffled, styles)
	m.rng.Shuffle(len(shuffled), func(i, j int) {
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	})

	return shuffled[:n]
}

// SelectRandomColorMood randomly selects one color mood from the available options
func (m *Manager) SelectRandomColorMood(moods []string) string {
	if len(moods) == 0 {
		return "modern color palette"
	}
	return moods[m.rng.Intn(len(moods))]
}

// FormatSuggestedElements formats the suggested elements as a comma-separated string
func FormatSuggestedElements(elements []string) string {
	if len(elements) == 0 {
		return "symbolic visual elements"
	}
	return strings.Join(elements, ", ")
}

// FormatStyles formats the selected styles as a descriptive string
func FormatStyles(styles []string) string {
	if len(styles) == 0 {
		return "modern illustration style"
	}
	if len(styles) == 1 {
		return styles[0] + " style"
	}
	return strings.Join(styles[:len(styles)-1], ", ") + " and " + styles[len(styles)-1] + " style"
}

