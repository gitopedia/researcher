package api

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// ConfigManager handles reading and writing research configuration
type ConfigManager struct {
	envPath string
}

// ResearchConfig represents the configurable research parameters
type ResearchConfig struct {
	// Research run parameters
	Iterations      int `json:"iterations"`
	MinImprovements int `json:"minImprovements"`
	MaxAttempts     int `json:"maxAttempts"`
	ArticleCount    int `json:"articleCount,omitempty"` // Not in env, but tracked for UI
	
	// Image generation
	GenerateImagesAfterRun bool `json:"generateImagesAfterRun"`
	GenerateSectionImages  bool `json:"generateSectionImages"`
	
	// Loop configuration
	LoopIntervalSeconds int `json:"loopIntervalSeconds"`
}

// NewConfigManager creates a new config manager
func NewConfigManager() *ConfigManager {
	return &ConfigManager{
		envPath: "config/.env",
	}
}

// GetConfig reads the current configuration
func (m *ConfigManager) GetConfig() ResearchConfig {
	config := ResearchConfig{
		Iterations:             getEnvInt("TOPIC_PROCESSING_ITERATIONS", 10),
		MinImprovements:        getEnvInt("IMPROVEMENTS_PER_NEW_ARTICLE", 10),
		MaxAttempts:            getEnvInt("MAX_IMPROVEMENT_ATTEMPTS", 20),
		GenerateImagesAfterRun: getEnvBool("GENERATE_IMAGES_AFTER_RUN", true),
		GenerateSectionImages:  getEnvBool("GENERATE_SECTION_IMAGES", false),
		LoopIntervalSeconds:    getEnvInt("LOOP_INTERVAL_SECONDS", 60),
	}
	return config
}

// UpdateConfig writes configuration changes to the .env file
func (m *ConfigManager) UpdateConfig(config ResearchConfig) error {
	// Read existing .env file or create new one
	envMap, err := m.readEnvFile()
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to read env file: %w", err)
	}
	if envMap == nil {
		envMap = make(map[string]string)
	}

	// Update values
	envMap["TOPIC_PROCESSING_ITERATIONS"] = strconv.Itoa(config.Iterations)
	envMap["IMPROVEMENTS_PER_NEW_ARTICLE"] = strconv.Itoa(config.MinImprovements)
	envMap["MAX_IMPROVEMENT_ATTEMPTS"] = strconv.Itoa(config.MaxAttempts)
	envMap["GENERATE_IMAGES_AFTER_RUN"] = strconv.FormatBool(config.GenerateImagesAfterRun)
	envMap["GENERATE_SECTION_IMAGES"] = strconv.FormatBool(config.GenerateSectionImages)
	envMap["LOOP_INTERVAL_SECONDS"] = strconv.Itoa(config.LoopIntervalSeconds)

	// Write back to file
	return m.writeEnvFile(envMap)
}

func (m *ConfigManager) readEnvFile() (map[string]string, error) {
	file, err := os.Open(m.envPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	envMap := make(map[string]string)
	scanner := bufio.NewScanner(file)
	
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		
		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Parse KEY=VALUE
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			envMap[key] = value
		}
	}

	return envMap, scanner.Err()
}

func (m *ConfigManager) writeEnvFile(envMap map[string]string) error {
	// Ensure directory exists
	dir := "config"
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	file, err := os.Create(m.envPath)
	if err != nil {
		return fmt.Errorf("failed to create env file: %w", err)
	}
	defer file.Close()

	// Write header
	file.WriteString("# Research Configuration (managed by dashboard)\n")
	file.WriteString("# This file overrides values from config/base.env\n\n")

	// Write values in a sensible order
	orderedKeys := []string{
		"TOPIC_PROCESSING_ITERATIONS",
		"IMPROVEMENTS_PER_NEW_ARTICLE",
		"MAX_IMPROVEMENT_ATTEMPTS",
		"GENERATE_IMAGES_AFTER_RUN",
		"GENERATE_SECTION_IMAGES",
		"LOOP_INTERVAL_SECONDS",
	}

	for _, key := range orderedKeys {
		if value, ok := envMap[key]; ok {
			file.WriteString(fmt.Sprintf("%s=%s\n", key, value))
		}
	}

	// Write any additional keys not in ordered list
	for key, value := range envMap {
		found := false
		for _, orderedKey := range orderedKeys {
			if key == orderedKey {
				found = true
				break
			}
		}
		if !found {
			file.WriteString(fmt.Sprintf("%s=%s\n", key, value))
		}
	}

	return nil
}

func getEnvInt(key string, defaultVal int) int {
	if val := os.Getenv(key); val != "" {
		if i, err := strconv.Atoi(val); err == nil {
			return i
		}
	}
	return defaultVal
}

func getEnvBool(key string, defaultVal bool) bool {
	if val := os.Getenv(key); val != "" {
		val = strings.ToLower(val)
		return val == "true" || val == "1" || val == "yes"
	}
	return defaultVal
}
