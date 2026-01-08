package styles

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// StylesConfig represents the artistic_styles.yaml structure
type StylesConfig struct {
	Default    map[string][]string            `yaml:"default"`
	Categories map[string]CategoryStyleConfig `yaml:",inline"`
}

// CategoryStyleConfig represents a category's style configuration
type CategoryStyleConfig struct {
	Header        []string                       `yaml:"header,omitempty"`
	Subcategories map[string]SubcategoryStyles   `yaml:",inline"`
}

// SubcategoryStyles represents subcategory-specific styles
type SubcategoryStyles struct {
	Header []string `yaml:"header,omitempty"`
}

// TemplatesConfig represents the image_prompt_templates.yaml structure
type TemplatesConfig struct {
	Default    TemplateEntry            `yaml:"default"`
	Categories map[string]CategoryEntry `yaml:",inline"`
}

// CategoryEntry represents a category's template configuration
type CategoryEntry struct {
	Header        TemplateEntry            `yaml:"header,omitempty"`
	Subcategories map[string]TemplateEntry `yaml:",inline"`
}

// TemplateEntry represents a single template configuration
type TemplateEntry struct {
	Header            *ImageTemplate `yaml:"header,omitempty"`
	Template          string         `yaml:"template,omitempty"`
	Guidance          string         `yaml:"guidance,omitempty"`
	SuggestedElements []string       `yaml:"suggested_elements,omitempty"`
	ColorMoods        []string       `yaml:"color_moods,omitempty"`
}

// ImageTemplate represents a complete image template
type ImageTemplate struct {
	Template          string   `yaml:"template,omitempty"`
	Guidance          string   `yaml:"guidance,omitempty"`
	SuggestedElements []string `yaml:"suggested_elements,omitempty"`
	ColorMoods        []string `yaml:"color_moods,omitempty"`
}

// ResolvedConfig contains the resolved styles and templates for a specific context
type ResolvedConfig struct {
	ArtisticStyles    []string
	Template          string
	Guidance          string
	SuggestedElements []string
	ColorMoods        []string
}

// Manager handles loading and resolving style configurations
type Manager struct {
	stylesPath    string
	templatesPath string
	styles        map[string]interface{}
	templates     map[string]interface{}
}

// NewManager creates a new style manager with the given config paths
func NewManager(configDir string) *Manager {
	return &Manager{
		stylesPath:    filepath.Join(configDir, "artistic_styles.yaml"),
		templatesPath: filepath.Join(configDir, "image_prompt_templates.yaml"),
	}
}

// Load reads and parses the configuration files
func (m *Manager) Load() error {
	// Load artistic styles
	stylesData, err := os.ReadFile(m.stylesPath)
	if err != nil {
		return fmt.Errorf("failed to read artistic_styles.yaml: %w", err)
	}

	m.styles = make(map[string]interface{})
	if err := yaml.Unmarshal(stylesData, &m.styles); err != nil {
		return fmt.Errorf("failed to parse artistic_styles.yaml: %w", err)
	}

	// Load templates
	templatesData, err := os.ReadFile(m.templatesPath)
	if err != nil {
		return fmt.Errorf("failed to read image_prompt_templates.yaml: %w", err)
	}

	m.templates = make(map[string]interface{})
	if err := yaml.Unmarshal(templatesData, &m.templates); err != nil {
		return fmt.Errorf("failed to parse image_prompt_templates.yaml: %w", err)
	}

	return nil
}

// ResolveAll resolves all configuration for a given context
func (m *Manager) ResolveAll(imageType, category, subcategory string) *ResolvedConfig {
	return &ResolvedConfig{
		ArtisticStyles:    m.resolveStyles(imageType, category, subcategory),
		Template:          m.resolveTemplate(imageType, category, subcategory),
		Guidance:          m.resolveGuidance(imageType, category, subcategory),
		SuggestedElements: m.resolveSuggestedElements(imageType, category, subcategory),
		ColorMoods:        m.resolveColorMoods(imageType, category, subcategory),
	}
}

// resolveStyles returns the most specific styles available for the context
func (m *Manager) resolveStyles(imageType, category, subcategory string) []string {
	// Try subcategory first
	if subcategory != "" {
		if styles := m.getStylesAt(category, subcategory, imageType); len(styles) > 0 {
			return styles
		}
	}

	// Try category
	if styles := m.getStylesAt(category, "", imageType); len(styles) > 0 {
		return styles
	}

	// Fall back to default
	return m.getStylesAt("default", "", imageType)
}

// getStylesAt retrieves styles at a specific path in the config
func (m *Manager) getStylesAt(category, subcategory, imageType string) []string {
	var node interface{}

	if category == "default" {
		if defaultNode, ok := m.styles["default"]; ok {
			node = defaultNode
		}
	} else {
		catNode, ok := m.styles[category]
		if !ok {
			return nil
		}

		if subcategory != "" {
			// Look for subcategory
			catMap, ok := catNode.(map[string]interface{})
			if !ok {
				return nil
			}
			subNode, ok := catMap[subcategory]
			if ok {
				node = subNode
			} else {
				node = catNode
			}
		} else {
			node = catNode
		}
	}

	if node == nil {
		return nil
	}

	// Extract styles for the image type
	nodeMap, ok := node.(map[string]interface{})
	if !ok {
		return nil
	}

	stylesList, ok := nodeMap[imageType]
	if !ok {
		return nil
	}

	// Convert to []string
	stylesSlice, ok := stylesList.([]interface{})
	if !ok {
		return nil
	}

	result := make([]string, 0, len(stylesSlice))
	for _, s := range stylesSlice {
		if str, ok := s.(string); ok {
			result = append(result, str)
		}
	}

	return result
}

// resolveTemplate returns the template for the given context
func (m *Manager) resolveTemplate(imageType, category, subcategory string) string {
	return m.resolveTemplateField(imageType, category, subcategory, "template")
}

// resolveGuidance returns the guidance for the given context
func (m *Manager) resolveGuidance(imageType, category, subcategory string) string {
	return m.resolveTemplateField(imageType, category, subcategory, "guidance")
}

// resolveTemplateField resolves a string field from templates config
func (m *Manager) resolveTemplateField(imageType, category, subcategory, field string) string {
	// Try subcategory first
	if subcategory != "" {
		if val := m.getTemplateFieldAt(category, subcategory, imageType, field); val != "" {
			return val
		}
	}

	// Try category
	if val := m.getTemplateFieldAt(category, "", imageType, field); val != "" {
		return val
	}

	// Fall back to default
	return m.getTemplateFieldAt("default", "", imageType, field)
}

// getTemplateFieldAt retrieves a template field at a specific path
func (m *Manager) getTemplateFieldAt(category, subcategory, imageType, field string) string {
	var node interface{}

	if category == "default" {
		if defaultNode, ok := m.templates["default"]; ok {
			node = defaultNode
		}
	} else {
		catNode, ok := m.templates[category]
		if !ok {
			return ""
		}

		if subcategory != "" {
			catMap, ok := catNode.(map[string]interface{})
			if !ok {
				return ""
			}
			subNode, ok := catMap[subcategory]
			if ok {
				node = subNode
			} else {
				node = catNode
			}
		} else {
			node = catNode
		}
	}

	if node == nil {
		return ""
	}

	nodeMap, ok := node.(map[string]interface{})
	if !ok {
		return ""
	}

	// Get the image type node (e.g., "header")
	typeNode, ok := nodeMap[imageType]
	if !ok {
		return ""
	}

	typeMap, ok := typeNode.(map[string]interface{})
	if !ok {
		return ""
	}

	if val, ok := typeMap[field]; ok {
		if str, ok := val.(string); ok {
			return str
		}
	}

	return ""
}

// resolveSuggestedElements returns suggested elements for the context
func (m *Manager) resolveSuggestedElements(imageType, category, subcategory string) []string {
	return m.resolveTemplateSliceField(imageType, category, subcategory, "suggested_elements")
}

// resolveColorMoods returns color moods for the context
func (m *Manager) resolveColorMoods(imageType, category, subcategory string) []string {
	return m.resolveTemplateSliceField(imageType, category, subcategory, "color_moods")
}

// resolveTemplateSliceField resolves a slice field from templates config
func (m *Manager) resolveTemplateSliceField(imageType, category, subcategory, field string) []string {
	// Try subcategory first
	if subcategory != "" {
		if val := m.getTemplateSliceFieldAt(category, subcategory, imageType, field); len(val) > 0 {
			return val
		}
	}

	// Try category
	if val := m.getTemplateSliceFieldAt(category, "", imageType, field); len(val) > 0 {
		return val
	}

	// Fall back to default
	return m.getTemplateSliceFieldAt("default", "", imageType, field)
}

// getTemplateSliceFieldAt retrieves a slice field at a specific path
func (m *Manager) getTemplateSliceFieldAt(category, subcategory, imageType, field string) []string {
	var node interface{}

	if category == "default" {
		if defaultNode, ok := m.templates["default"]; ok {
			node = defaultNode
		}
	} else {
		catNode, ok := m.templates[category]
		if !ok {
			return nil
		}

		if subcategory != "" {
			catMap, ok := catNode.(map[string]interface{})
			if !ok {
				return nil
			}
			subNode, ok := catMap[subcategory]
			if ok {
				node = subNode
			} else {
				node = catNode
			}
		} else {
			node = catNode
		}
	}

	if node == nil {
		return nil
	}

	nodeMap, ok := node.(map[string]interface{})
	if !ok {
		return nil
	}

	typeNode, ok := nodeMap[imageType]
	if !ok {
		return nil
	}

	typeMap, ok := typeNode.(map[string]interface{})
	if !ok {
		return nil
	}

	if val, ok := typeMap[field]; ok {
		if sliceVal, ok := val.([]interface{}); ok {
			result := make([]string, 0, len(sliceVal))
			for _, item := range sliceVal {
				if str, ok := item.(string); ok {
					result = append(result, str)
				}
			}
			return result
		}
	}

	return nil
}

// SelectRandomStyles selects n random styles from the available styles
func (m *Manager) SelectRandomStyles(styles []string, n int) []string {
	if len(styles) == 0 {
		return nil
	}
	if n >= len(styles) {
		return styles
	}

	// Shuffle and take first n
	shuffled := make([]string, len(styles))
	copy(shuffled, styles)
	rand.Shuffle(len(shuffled), func(i, j int) {
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	})

	return shuffled[:n]
}

// SelectRandomColorMood selects a random color mood from the available moods
func (m *Manager) SelectRandomColorMood(moods []string) string {
	if len(moods) == 0 {
		return "balanced and harmonious"
	}
	return moods[rand.Intn(len(moods))]
}

