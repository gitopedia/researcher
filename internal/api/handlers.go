package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	gh "github.com/gitopedia/researcher/internal/github"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for local development
	},
}

// Status handlers

func (s *Server) handleGetStatus(w http.ResponseWriter, r *http.Request) {
	status := s.status.GetFullStatus()
	respondJSON(w, http.StatusOK, status)
}

func (s *Server) handleServicesStatus(w http.ResponseWriter, r *http.Request) {
	status := struct {
		Docker  DockerStatus  `json:"docker"`
		Ollama  OllamaStatus  `json:"ollama"`
		ComfyUI ComfyUIStatus `json:"comfyui"`
	}{
		Docker:  s.status.GetDockerStatus(),
		Ollama:  s.status.GetOllamaStatus(),
		ComfyUI: s.status.GetComfyUIStatus(),
	}
	respondJSON(w, http.StatusOK, status)
}

// Researcher handlers

func (s *Server) handleResearcherStatus(w http.ResponseWriter, r *http.Request) {
	// If the backend restarted while a run was active, reconcile from PID file so UI is accurate
	s.runner.Reconcile()
	status := s.runner.GetStatus()
	respondJSON(w, http.StatusOK, status)
}

func (s *Server) handleResearcherStart(w http.ResponseWriter, r *http.Request) {
	var config RunConfig
	if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if config.Mode == "" {
		config.Mode = ModeFull
	}

	if err := s.runner.Start(config); err != nil {
		respondError(w, http.StatusConflict, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"status": "started"})
}

func (s *Server) handleResearcherPause(w http.ResponseWriter, r *http.Request) {
	if err := s.runner.Pause(); err != nil {
		respondError(w, http.StatusConflict, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "paused"})
}

func (s *Server) handleResearcherResume(w http.ResponseWriter, r *http.Request) {
	if err := s.runner.Resume(); err != nil {
		respondError(w, http.StatusConflict, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "resumed"})
}

func (s *Server) handleResearcherStop(w http.ResponseWriter, r *http.Request) {
	if err := s.runner.Stop(); err != nil {
		respondError(w, http.StatusConflict, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "stopping"})
}

// Service control handlers

func (s *Server) handleDockerStart(w http.ResponseWriter, r *http.Request) {
	if err := StartDocker(); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "starting"})
}

func (s *Server) handleDockerStop(w http.ResponseWriter, r *http.Request) {
	if err := StopDocker(); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "stopping"})
}

func (s *Server) handleOllamaStart(w http.ResponseWriter, r *http.Request) {
	if err := StartOllama(); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "starting"})
}

func (s *Server) handleOllamaStop(w http.ResponseWriter, r *http.Request) {
	if err := StopOllama(); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "stopping"})
}

func (s *Server) handleComfyUIStart(w http.ResponseWriter, r *http.Request) {
	if err := StartComfyUI(); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "starting"})
}

func (s *Server) handleComfyUIStop(w http.ResponseWriter, r *http.Request) {
	if err := StopComfyUI(); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "stopping"})
}

// Git handlers

func (s *Server) handleGitBranch(w http.ResponseWriter, r *http.Request) {
	info, err := s.gitMgr.GetBranchInfo()
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, info)
}

func (s *Server) handleGitClean(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CleanImages   bool `json:"cleanImages"`
		CleanArticles bool `json:"cleanArticles"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if err := s.gitMgr.Clean(req.CleanImages, req.CleanArticles); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"status": "cleaned"})
}

// Config handlers

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	config := s.config.GetConfig()
	respondJSON(w, http.StatusOK, config)
}

func (s *Server) handleUpdateConfig(w http.ResponseWriter, r *http.Request) {
	var config ResearchConfig
	if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if err := s.config.UpdateConfig(config); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// Topics handlers

// TopicInfo represents metadata about an incoming article
type TopicInfo struct {
	Slug     string `json:"slug"`
	Title    string `json:"title"`
	Domain   string `json:"domain"`
	Category string `json:"category"`
	Topic    string `json:"topic"`
}

func (s *Server) handleListTopics(w http.ResponseWriter, r *http.Request) {
	incomingPath := filepath.Join(s.repoPath, "Compendium", "_incoming")
	
	entries, err := os.ReadDir(incomingPath)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to read incoming directory")
		return
	}

	var topics []TopicInfo
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		slug := strings.TrimSuffix(entry.Name(), ".md")
		
		// Read file to extract frontmatter
		content, err := os.ReadFile(filepath.Join(incomingPath, entry.Name()))
		if err != nil {
			continue
		}

		topic := TopicInfo{
			Slug:     slug,
			Title:    extractFrontmatterValue(string(content), "article"),
			Domain:   extractFrontmatterValue(string(content), "domain"),
			Category: extractFrontmatterValue(string(content), "category"),
			Topic:    extractFrontmatterValue(string(content), "topic"),
		}
		if topic.Title == "" {
			topic.Title = slug
		}

		topics = append(topics, topic)
	}

	// Sort by title
	sort.Slice(topics, func(i, j int) bool {
		return topics[i].Title < topics[j].Title
	})

	respondJSON(w, http.StatusOK, topics)
}

func (s *Server) handleGetTopic(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	slug := vars["slug"]

	filePath := filepath.Join(s.repoPath, "Compendium", "_incoming", slug+".md")
	
	content, err := os.ReadFile(filePath)
	if err != nil {
		respondError(w, http.StatusNotFound, "Topic not found")
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{
		"slug":    slug,
		"content": string(content),
	})
}

// Images handlers

// ImageGroup represents a group of images for an article or index
type ImageGroup struct {
	Name   string   `json:"name"`
	Type   string   `json:"type"` // "article", "domain", "category", "topic"
	Path   string   `json:"path"`
	Images []string `json:"images"`
}

func (s *Server) handleListImages(w http.ResponseWriter, r *http.Request) {
	incomingPath := filepath.Join(s.repoPath, "Compendium", "_incoming")
	
	groups := make(map[string]*ImageGroup)

	// Scan root _incoming for article images
	entries, _ := os.ReadDir(incomingPath)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".png") {
			continue
		}

		// Extract base name (remove _header_1.png, _header_2.png, etc.)
		name := entry.Name()
		baseName := extractImageBaseName(name)

		if _, exists := groups[baseName]; !exists {
			groups[baseName] = &ImageGroup{
				Name:   baseName,
				Type:   "article",
				Path:   "",
				Images: []string{},
			}
		}
		groups[baseName].Images = append(groups[baseName].Images, name)
	}

	// Scan indexes subdirectories
	indexTypes := []struct {
		dir  string
		typ  string
	}{
		{"indexes/domains", "domain"},
		{"indexes/categories", "category"},
		{"indexes/topics", "topic"},
	}

	for _, it := range indexTypes {
		indexPath := filepath.Join(incomingPath, it.dir)
		entries, err := os.ReadDir(indexPath)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".png") {
				continue
			}

			name := entry.Name()
			baseName := extractImageBaseName(name)

			key := it.dir + "/" + baseName
			if _, exists := groups[key]; !exists {
				groups[key] = &ImageGroup{
					Name:   baseName,
					Type:   it.typ,
					Path:   it.dir,
					Images: []string{},
				}
			}
			groups[key].Images = append(groups[key].Images, name)
		}
	}

	// Convert to slice and sort
	var result []ImageGroup
	for _, g := range groups {
		// Sort images within group
		sort.Strings(g.Images)
		result = append(result, *g)
	}

	// Sort by type then name
	sort.Slice(result, func(i, j int) bool {
		if result[i].Type != result[j].Type {
			typeOrder := map[string]int{"domain": 0, "category": 1, "topic": 2, "article": 3}
			return typeOrder[result[i].Type] < typeOrder[result[j].Type]
		}
		return result[i].Name < result[j].Name
	})

	respondJSON(w, http.StatusOK, result)
}

func (s *Server) handleGetImage(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	imagePath := vars["path"]

	// Sanitize path to prevent directory traversal
	imagePath = filepath.Clean(imagePath)
	if strings.Contains(imagePath, "..") {
		respondError(w, http.StatusBadRequest, "Invalid path")
		return
	}

	fullPath := filepath.Join(s.repoPath, "Compendium", "_incoming", imagePath)

	// Check if file exists
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		respondError(w, http.StatusNotFound, "Image not found")
		return
	}

	// Serve the file
	http.ServeFile(w, r, fullPath)
}

// handleDeleteImageGroup deletes all images for a domain/category/topic/article
func (s *Server) handleDeleteImageGroup(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	groupType := vars["type"]   // "domain", "category", "topic", or "article"
	groupName := vars["name"]   // e.g., "science", "science--physics", "turbocharging"

	log.Printf("[Delete Group] Request: type=%s, name=%s", groupType, groupName)

	deletedCount := 0
	var errors []string

	// Determine the path based on type
	var searchPaths []string
	var promptBasePath string

	switch groupType {
	case "domain":
		searchPaths = []string{filepath.Join(s.repoPath, "Compendium", "_incoming", "indexes", "domains")}
		promptBasePath = filepath.Join(s.repoPath, "Compendium", "_debug", "indexes", groupName)
	case "category":
		searchPaths = []string{filepath.Join(s.repoPath, "Compendium", "_incoming", "indexes", "categories")}
		// Extract domain from name (e.g., "science--physics" -> domain="science", category="physics")
		parts := strings.SplitN(groupName, "--", 2)
		if len(parts) == 2 {
			promptBasePath = filepath.Join(s.repoPath, "Compendium", "_debug", "indexes", parts[0], parts[1])
		}
	case "topic":
		searchPaths = []string{filepath.Join(s.repoPath, "Compendium", "_incoming", "indexes", "topics")}
		// Extract domain/category/topic from name
		parts := strings.SplitN(groupName, "--", 3)
		if len(parts) == 3 {
			promptBasePath = filepath.Join(s.repoPath, "Compendium", "_debug", "indexes", parts[0], parts[1], parts[2])
		}
	case "article":
		searchPaths = []string{filepath.Join(s.repoPath, "Compendium", "_incoming")}
		promptBasePath = filepath.Join(s.repoPath, "Compendium", "_debug", "articles", groupName)
	default:
		log.Printf("[Delete Group] Invalid group type: %s", groupType)
		respondError(w, http.StatusBadRequest, fmt.Sprintf("Invalid group type: %s", groupType))
		return
	}

	log.Printf("[Delete Group] Search paths: %v", searchPaths)

	// Find and delete matching images
	for _, searchPath := range searchPaths {
		entries, err := os.ReadDir(searchPath)
		if err != nil {
			log.Printf("[Delete Group] Failed to read directory %s: %v", searchPath, err)
			continue
		}

		log.Printf("[Delete Group] Found %d entries in %s", len(entries), searchPath)

		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".png") {
				continue
			}

			// Check if this image belongs to the group
			baseName := extractImageBaseName(entry.Name())
			log.Printf("[Delete Group] Checking %s: baseName=%s, groupName=%s, match=%v", 
				entry.Name(), baseName, groupName, baseName == groupName)
			
			if baseName == groupName {
				imagePath := filepath.Join(searchPath, entry.Name())
				if err := os.Remove(imagePath); err != nil {
					errors = append(errors, fmt.Sprintf("Failed to delete %s: %v", entry.Name(), err))
					log.Printf("[Delete Group] Failed to delete %s: %v", imagePath, err)
				} else {
					deletedCount++
					log.Printf("[Delete Group] Deleted %s", imagePath)

					// Also delete corresponding prompt file
					candidateIdx := extractCandidateIndex(entry.Name())
					if candidateIdx > 0 {
						promptFileName := fmt.Sprintf("header_image_prompt_%d.txt", candidateIdx)
						promptPath := filepath.Join(promptBasePath, promptFileName)
						os.Remove(promptPath) // Ignore error if doesn't exist
					}
				}
			}
		}
	}

	// Delete any prompt files in the debug directory for complete regeneration
	if promptBasePath != "" {
		entries, err := os.ReadDir(promptBasePath)
		if err == nil {
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), "header_image_prompt_") && strings.HasSuffix(entry.Name(), ".txt") {
					promptPath := filepath.Join(promptBasePath, entry.Name())
					os.Remove(promptPath)
				}
			}
		}
	}

	log.Printf("[Delete Group] Completed: deleted=%d, errors=%d", deletedCount, len(errors))

	response := map[string]interface{}{
		"deleted": deletedCount,
		"errors":  errors,
	}
	respondJSON(w, http.StatusOK, response)
}

// handleDeleteImage deletes a single image file
func (s *Server) handleDeleteImage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Sanitize path to prevent directory traversal
	imagePath := filepath.Clean(req.Path)
	if strings.Contains(imagePath, "..") {
		respondError(w, http.StatusBadRequest, "Invalid path")
		return
	}

	fullPath := filepath.Join(s.repoPath, "Compendium", "_incoming", imagePath)

	// Check if file exists
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		respondError(w, http.StatusNotFound, "Image not found")
		return
	}

	// Determine the prompt path to delete
	// Image path can be:
	// - "article_header_5.png" (article in root)
	// - "indexes/domains/science_header_3.png"
	// - "indexes/categories/science--physics_header_2.png"
	// - "indexes/topics/science--physics--quantum-mechanics_header_1.png"

	filename := filepath.Base(imagePath)
	baseName := extractImageBaseName(filename)
	candidateIdx := extractCandidateIndex(filename)

	var promptPath string
	if strings.HasPrefix(imagePath, "indexes/domains/") {
		promptPath = filepath.Join(s.repoPath, "Compendium", "_debug", "indexes", baseName)
	} else if strings.HasPrefix(imagePath, "indexes/categories/") {
		parts := strings.SplitN(baseName, "--", 2)
		if len(parts) == 2 {
			promptPath = filepath.Join(s.repoPath, "Compendium", "_debug", "indexes", parts[0], parts[1])
		}
	} else if strings.HasPrefix(imagePath, "indexes/topics/") {
		parts := strings.SplitN(baseName, "--", 3)
		if len(parts) == 3 {
			promptPath = filepath.Join(s.repoPath, "Compendium", "_debug", "indexes", parts[0], parts[1], parts[2])
		}
	} else {
		// Article image
		promptPath = filepath.Join(s.repoPath, "Compendium", "_debug", "articles", baseName)
	}

	// Delete the image file
	if err := os.Remove(fullPath); err != nil {
		respondError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to delete image: %v", err))
		return
	}

	// Delete the corresponding prompt file so backfill will regenerate
	if promptPath != "" && candidateIdx > 0 {
		promptFileName := fmt.Sprintf("header_image_prompt_%d.txt", candidateIdx)
		promptFilePath := filepath.Join(promptPath, promptFileName)
		os.Remove(promptFilePath) // Ignore error if doesn't exist
	}

	respondJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ImageSelections represents the structure for tracking selected images
type ImageSelections struct {
	Articles map[string]string `json:"articles"`
	Indexes  IndexSelections   `json:"indexes"`
}

type IndexSelections struct {
	Domains    map[string]string `json:"domains"`
	Categories map[string]string `json:"categories"`
	Topics     map[string]string `json:"topics"`
}

func (s *Server) getSelectionsPath() string {
	return filepath.Join(s.repoPath, "Compendium", "_debug", "image_selections.json")
}

func (s *Server) loadSelections() (*ImageSelections, error) {
	selectionsPath := s.getSelectionsPath()
	
	data, err := os.ReadFile(selectionsPath)
	if os.IsNotExist(err) {
		// Return empty selections if file doesn't exist
		return &ImageSelections{
			Articles: make(map[string]string),
			Indexes: IndexSelections{
				Domains:    make(map[string]string),
				Categories: make(map[string]string),
				Topics:     make(map[string]string),
			},
		}, nil
	}
	if err != nil {
		return nil, err
	}
	
	var selections ImageSelections
	if err := json.Unmarshal(data, &selections); err != nil {
		return nil, err
	}
	
	// Ensure maps are initialized
	if selections.Articles == nil {
		selections.Articles = make(map[string]string)
	}
	if selections.Indexes.Domains == nil {
		selections.Indexes.Domains = make(map[string]string)
	}
	if selections.Indexes.Categories == nil {
		selections.Indexes.Categories = make(map[string]string)
	}
	if selections.Indexes.Topics == nil {
		selections.Indexes.Topics = make(map[string]string)
	}
	
	return &selections, nil
}

func (s *Server) saveSelections(selections *ImageSelections) error {
	selectionsPath := s.getSelectionsPath()
	
	// Ensure _debug directory exists
	debugDir := filepath.Dir(selectionsPath)
	if err := os.MkdirAll(debugDir, 0755); err != nil {
		return err
	}
	
	data, err := json.MarshalIndent(selections, "", "  ")
	if err != nil {
		return err
	}
	
	return os.WriteFile(selectionsPath, data, 0644)
}

func (s *Server) handleGetImageSelections(w http.ResponseWriter, r *http.Request) {
	selections, err := s.loadSelections()
	if err != nil {
		respondError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to load selections: %v", err))
		return
	}
	
	respondJSON(w, http.StatusOK, selections)
}

func (s *Server) handleUpdateImageSelection(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Type     string `json:"type"`     // "article", "domain", "category", "topic"
		Name     string `json:"name"`     // base name (e.g., "turbocharging", "science--physics")
		Filename string `json:"filename"` // selected filename (e.g., "turbocharging_header_3.png")
	}
	
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	
	if req.Type == "" || req.Name == "" || req.Filename == "" {
		respondError(w, http.StatusBadRequest, "Missing required fields: type, name, filename")
		return
	}
	
	selections, err := s.loadSelections()
	if err != nil {
		respondError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to load selections: %v", err))
		return
	}
	
	// Update the appropriate selection
	switch req.Type {
	case "article":
		selections.Articles[req.Name] = req.Filename
	case "domain":
		selections.Indexes.Domains[req.Name] = req.Filename
	case "category":
		selections.Indexes.Categories[req.Name] = req.Filename
	case "topic":
		selections.Indexes.Topics[req.Name] = req.Filename
	default:
		respondError(w, http.StatusBadRequest, fmt.Sprintf("Invalid type: %s", req.Type))
		return
	}
	
	if err := s.saveSelections(selections); err != nil {
		respondError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to save selections: %v", err))
		return
	}
	
	respondJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// FinalizeResult represents the result of the finalization process
type FinalizeResult struct {
	Renamed   []string `json:"renamed"`
	Converted []string `json:"converted"`
	Deleted   []string `json:"deleted"`
	Errors    []string `json:"errors"`
}

func (s *Server) handleFinalize(w http.ResponseWriter, r *http.Request) {
	selections, err := s.loadSelections()
	if err != nil {
		respondError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to load selections: %v", err))
		return
	}
	
	result := FinalizeResult{
		Renamed:   []string{},
		Converted: []string{},
		Deleted:   []string{},
		Errors:    []string{},
	}
	
	incomingPath := filepath.Join(s.repoPath, "Compendium", "_incoming")
	
	// Collect PNG files to convert after renaming
	var pngFilesToConvert []string
	
	// Process article images in _incoming root
	s.finalizeImageGroup(incomingPath, "", selections.Articles, &result, &pngFilesToConvert)
	
	// Process index images
	indexPaths := []struct {
		dir        string
		selections map[string]string
	}{
		{"indexes/domains", selections.Indexes.Domains},
		{"indexes/categories", selections.Indexes.Categories},
		{"indexes/topics", selections.Indexes.Topics},
	}
	
	for _, idx := range indexPaths {
		indexDir := filepath.Join(incomingPath, idx.dir)
		s.finalizeImageGroup(indexDir, idx.dir, idx.selections, &result, &pngFilesToConvert)
	}
	
	// Convert PNG files to AVIF
	if len(pngFilesToConvert) > 0 {
		s.convertPNGsToAVIF(pngFilesToConvert, &result)
	}
	
	// Clean up the selections file after finalization
	selectionsPath := s.getSelectionsPath()
	os.Remove(selectionsPath)
	
	respondJSON(w, http.StatusOK, result)
}

// convertPNGsToAVIF converts PNG files to AVIF format using the Node.js script
func (s *Server) convertPNGsToAVIF(pngFiles []string, result *FinalizeResult) {
	// Get the path to the conversion script
	// The script is in the researcher/scripts folder relative to the executable
	execPath, err := os.Executable()
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("Failed to get executable path: %v", err))
		return
	}
	
	// Script path - try multiple locations
	scriptPaths := []string{
		filepath.Join(filepath.Dir(execPath), "scripts", "convert-to-avif.js"),
		filepath.Join(filepath.Dir(execPath), "..", "scripts", "convert-to-avif.js"),
		filepath.Join(s.repoPath, "..", "researcher", "scripts", "convert-to-avif.js"),
	}
	
	var scriptPath string
	for _, p := range scriptPaths {
		if _, err := os.Stat(p); err == nil {
			scriptPath = p
			break
		}
	}
	
	if scriptPath == "" {
		// Try to find it relative to working directory
		cwd, _ := os.Getwd()
		scriptPath = filepath.Join(cwd, "scripts", "convert-to-avif.js")
		if _, err := os.Stat(scriptPath); err != nil {
			result.Errors = append(result.Errors, "Could not find convert-to-avif.js script")
			return
		}
	}
	
	// Determine node command based on OS
	nodeCmd := "node"
	if runtime.GOOS == "windows" {
		nodeCmd = "node.exe"
	}
	
	// Convert each file
	for _, pngPath := range pngFiles {
		cmd := exec.Command(nodeCmd, scriptPath, pngPath)
		output, err := cmd.CombinedOutput()
		
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("Failed to convert %s: %v - %s", filepath.Base(pngPath), err, string(output)))
			continue
		}
		
		// Parse the output to get the result
		baseName := filepath.Base(pngPath)
		avifName := strings.TrimSuffix(baseName, ".png") + ".avif"
		result.Converted = append(result.Converted, fmt.Sprintf("%s -> %s", baseName, avifName))
	}
}

func (s *Server) finalizeImageGroup(dirPath, relativePath string, selections map[string]string, result *FinalizeResult, pngFilesToConvert *[]string) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return // Directory might not exist, that's fine
	}
	
	// Group images by base name
	imageGroups := make(map[string][]string)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".png") {
			continue
		}
		baseName := extractImageBaseName(entry.Name())
		imageGroups[baseName] = append(imageGroups[baseName], entry.Name())
	}
	
	// Process each group
	for baseName, images := range imageGroups {
		selectedFilename := selections[baseName]
		
		// If no selection made, skip this group (keep all variants)
		if selectedFilename == "" {
			continue
		}
		
		// Verify the selected file exists in the group
		selectedExists := false
		for _, img := range images {
			if img == selectedFilename {
				selectedExists = true
				break
			}
		}
		
		if !selectedExists {
			result.Errors = append(result.Errors, fmt.Sprintf("Selected file %s not found for %s", selectedFilename, baseName))
			continue
		}
		
		// Determine the canonical filename (without _N suffix)
		// e.g., "turbocharging_header_3.png" -> "turbocharging_header.png"
		canonicalName := s.getCanonicalImageName(selectedFilename)
		
		// Track the final PNG path for conversion
		var finalPngPath string
		
		// Rename selected file to canonical name (if different)
		if selectedFilename != canonicalName {
			srcPath := filepath.Join(dirPath, selectedFilename)
			dstPath := filepath.Join(dirPath, canonicalName)
			
			if err := os.Rename(srcPath, dstPath); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("Failed to rename %s: %v", selectedFilename, err))
				continue
			}
			
			displayPath := canonicalName
			if relativePath != "" {
				displayPath = relativePath + "/" + canonicalName
			}
			result.Renamed = append(result.Renamed, fmt.Sprintf("%s -> %s", selectedFilename, displayPath))
			finalPngPath = dstPath
		} else {
			finalPngPath = filepath.Join(dirPath, selectedFilename)
		}
		
		// Add to conversion list
		*pngFilesToConvert = append(*pngFilesToConvert, finalPngPath)
		
		// Delete all other variants
		for _, img := range images {
			if img == selectedFilename {
				continue // Skip the selected file (already renamed)
			}
			
			imgPath := filepath.Join(dirPath, img)
			if err := os.Remove(imgPath); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("Failed to delete %s: %v", img, err))
				continue
			}
			
			displayPath := img
			if relativePath != "" {
				displayPath = relativePath + "/" + img
			}
			result.Deleted = append(result.Deleted, displayPath)
		}
	}
}

// getCanonicalImageName removes the numeric suffix from an image filename
// e.g., "turbocharging_header_3.png" -> "turbocharging_header.png"
func (s *Server) getCanonicalImageName(filename string) string {
	// Remove extension
	name := strings.TrimSuffix(filename, ".png")
	
	// Split by underscore
	parts := strings.Split(name, "_")
	
	// Check if last part is numeric
	if len(parts) > 0 && isNumeric(parts[len(parts)-1]) {
		// Remove the numeric suffix
		parts = parts[:len(parts)-1]
	}
	
	return strings.Join(parts, "_") + ".png"
}

// OrganizeResult represents the result of the organize operation
type OrganizeResult struct {
	Success        bool     `json:"success"`
	Message        string   `json:"message"`
	ArticlesMoved  []string `json:"articlesMoved,omitempty"`
	ImagesMoved    []string `json:"imagesMoved,omitempty"`
	IndexesUpdated []string `json:"indexesUpdated,omitempty"`
	Errors         []string `json:"errors,omitempty"`
}

func (s *Server) handleOrganize(w http.ResponseWriter, r *http.Request) {
	// Get current branch name
	branchCmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	branchCmd.Dir = s.repoPath
	branchOutput, err := branchCmd.Output()
	if err != nil {
		respondError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to get current branch: %v", err))
		return
	}
	
	branchName := strings.TrimSpace(string(branchOutput))
	
	// Don't allow organizing on main branch
	if branchName == "main" || branchName == "master" {
		respondError(w, http.StatusBadRequest, "Cannot organize articles on main branch. Please checkout a research branch first.")
		return
	}
	
	log.Printf("[Organize] Running local organize on branch: %s", branchName)
	
	result := s.organizeLocal()
	
	if len(result.Errors) > 0 {
		result.Success = false
		result.Message = fmt.Sprintf("Organization completed with %d errors", len(result.Errors))
	} else {
		result.Success = true
		result.Message = "Articles organized successfully"
	}
	
	respondJSON(w, http.StatusOK, result)
}

// organizeLocal performs local filesystem organization of articles and images
func (s *Server) organizeLocal() OrganizeResult {
	result := OrganizeResult{
		ArticlesMoved:  []string{},
		ImagesMoved:    []string{},
		IndexesUpdated: []string{},
		Errors:         []string{},
	}
	
	incomingPath := filepath.Join(s.repoPath, "Compendium", "_incoming")
	
	// Step 1: Find and process all markdown articles in _incoming
	entries, err := os.ReadDir(incomingPath)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("Failed to read _incoming: %v", err))
		return result
	}
	
	type articleInfo struct {
		filename     string
		slug         string
		domain       string
		domainSlug   string
		category     string
		categorySlug string
		topic        string
		topicSlug    string
	}
	
	var articles []articleInfo
	
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		
		// Read and parse frontmatter
		filePath := filepath.Join(incomingPath, entry.Name())
		content, err := os.ReadFile(filePath)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("Failed to read %s: %v", entry.Name(), err))
			continue
		}
		
		// Parse frontmatter
		info := articleInfo{filename: entry.Name()}
		info.slug = strings.TrimSuffix(entry.Name(), ".md")
		info.domainSlug = extractFrontmatterValue(string(content), "domain-slug")
		info.categorySlug = extractFrontmatterValue(string(content), "category-slug")
		info.topicSlug = extractFrontmatterValue(string(content), "topic-slug")
		info.domain = extractFrontmatterValue(string(content), "domain")
		info.category = extractFrontmatterValue(string(content), "category")
		info.topic = extractFrontmatterValue(string(content), "topic")
		
		if info.domainSlug == "" || info.categorySlug == "" || info.topicSlug == "" {
			result.Errors = append(result.Errors, fmt.Sprintf("Missing frontmatter in %s", entry.Name()))
			continue
		}
		
		articles = append(articles, info)
	}
	
	// Step 2: Move articles and their images
	for _, article := range articles {
		targetDir := filepath.Join(s.repoPath, "Compendium", article.domainSlug, article.categorySlug, article.topicSlug)
		imgDir := filepath.Join(targetDir, "_img")
		
		// Create target directories
		if err := os.MkdirAll(imgDir, 0755); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("Failed to create directory %s: %v", targetDir, err))
			continue
		}
		
		// Read article content and update image references
		srcArticle := filepath.Join(incomingPath, article.filename)
		content, err := os.ReadFile(srcArticle)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("Failed to read %s: %v", article.filename, err))
			continue
		}
		
		// Update image reference from slug_header.avif to _img/slug_header.avif
		updatedContent := s.updateImageReferences(string(content), article.slug)
		
		// Write article to target location
		dstArticle := filepath.Join(targetDir, article.filename)
		if err := os.WriteFile(dstArticle, []byte(updatedContent), 0644); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("Failed to write %s: %v", dstArticle, err))
			continue
		}
		
		// Delete source article
		os.Remove(srcArticle)
		result.ArticlesMoved = append(result.ArticlesMoved, fmt.Sprintf("%s -> %s/%s/%s/", article.slug, article.domainSlug, article.categorySlug, article.topicSlug))
		
		// Move article images (both .avif and -medium.avif)
		for _, suffix := range []string{"_header.avif", "_header-medium.avif"} {
			srcImg := filepath.Join(incomingPath, article.slug+suffix)
			if _, err := os.Stat(srcImg); err == nil {
				dstImg := filepath.Join(imgDir, article.slug+suffix)
				if err := os.Rename(srcImg, dstImg); err != nil {
					result.Errors = append(result.Errors, fmt.Sprintf("Failed to move %s: %v", article.slug+suffix, err))
				} else {
					result.ImagesMoved = append(result.ImagesMoved, article.slug+suffix)
				}
			}
		}
	}
	
	// Step 3: Move index images
	indexMappings := []struct {
		srcDir   string
		parseKey func(filename string) (dstDir string, ok bool)
	}{
		{
			srcDir: filepath.Join(incomingPath, "indexes", "domains"),
			parseKey: func(filename string) (string, bool) {
				base := strings.TrimSuffix(strings.TrimSuffix(filename, ".avif"), "-medium")
				base = strings.TrimSuffix(base, "_header")
				return filepath.Join(s.repoPath, "Compendium", base, "_img"), true
			},
		},
		{
			srcDir: filepath.Join(incomingPath, "indexes", "categories"),
			parseKey: func(filename string) (string, bool) {
				base := strings.TrimSuffix(strings.TrimSuffix(filename, ".avif"), "-medium")
				base = strings.TrimSuffix(base, "_header")
				parts := strings.SplitN(base, "--", 2)
				if len(parts) != 2 {
					return "", false
				}
				return filepath.Join(s.repoPath, "Compendium", parts[0], parts[1], "_img"), true
			},
		},
		{
			srcDir: filepath.Join(incomingPath, "indexes", "topics"),
			parseKey: func(filename string) (string, bool) {
				base := strings.TrimSuffix(strings.TrimSuffix(filename, ".avif"), "-medium")
				base = strings.TrimSuffix(base, "_header")
				parts := strings.SplitN(base, "--", 3)
				if len(parts) != 3 {
					return "", false
				}
				return filepath.Join(s.repoPath, "Compendium", parts[0], parts[1], parts[2], "_img"), true
			},
		},
	}
	
	for _, mapping := range indexMappings {
		entries, err := os.ReadDir(mapping.srcDir)
		if err != nil {
			continue // Directory might not exist
		}
		
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".avif") {
				continue
			}
			
			dstDir, ok := mapping.parseKey(entry.Name())
			if !ok {
				result.Errors = append(result.Errors, fmt.Sprintf("Failed to parse index image: %s", entry.Name()))
				continue
			}
			
			// Create target directory
			if err := os.MkdirAll(dstDir, 0755); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("Failed to create %s: %v", dstDir, err))
				continue
			}
			
			// Move image
			srcPath := filepath.Join(mapping.srcDir, entry.Name())
			dstPath := filepath.Join(dstDir, entry.Name())
			if err := os.Rename(srcPath, dstPath); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("Failed to move %s: %v", entry.Name(), err))
			} else {
				result.ImagesMoved = append(result.ImagesMoved, entry.Name())
			}
		}
	}
	
	// Step 4: Clean up _incoming and _debug folders
	os.RemoveAll(incomingPath)
	os.RemoveAll(filepath.Join(s.repoPath, "Compendium", "_debug"))
	os.RemoveAll(filepath.Join(s.repoPath, "Compendium", "_config"))
	
	return result
}

// updateImageReferences updates image paths in article content
func (s *Server) updateImageReferences(content, articleSlug string) string {
	// Update header image reference
	// From: ![Header](slug_header.avif) or ![Header](slug_header.png)
	// To: ![Header](_img/slug_header.avif)
	
	patterns := []struct {
		old string
		new string
	}{
		{fmt.Sprintf("![Header](%s_header.avif)", articleSlug), fmt.Sprintf("![Header](_img/%s_header.avif)", articleSlug)},
		{fmt.Sprintf("![Header](%s_header.png)", articleSlug), fmt.Sprintf("![Header](_img/%s_header.avif)", articleSlug)},
	}
	
	for _, p := range patterns {
		content = strings.ReplaceAll(content, p.old, p.new)
	}
	
	return content
}

// extractCandidateIndex extracts the candidate number from an image filename
// e.g., "turbocharging_header_5.png" -> 5
func extractCandidateIndex(filename string) int {
	// Remove extension
	name := strings.TrimSuffix(filename, ".png")
	
	// Find the last underscore followed by a number
	parts := strings.Split(name, "_")
	if len(parts) > 0 {
		lastPart := parts[len(parts)-1]
		if isNumeric(lastPart) {
			var idx int
			fmt.Sscanf(lastPart, "%d", &idx)
			return idx
		}
	}
	return 0
}

// WebSocket handler

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	// Register client
	s.wsClientsMu.Lock()
	s.wsClients[conn] = true
	s.wsClientsMu.Unlock()

	// Send initial status
	status := s.status.GetFullStatus()
	conn.WriteJSON(StatusUpdate{
		Type:    "status",
		Payload: status,
	})

	runnerStatus := s.runner.GetStatus()
	conn.WriteJSON(StatusUpdate{
		Type:    "researcher",
		Payload: runnerStatus,
	})

	// Handle client messages (for future use)
	go func() {
		defer func() {
			s.wsClientsMu.Lock()
			delete(s.wsClients, conn)
			s.wsClientsMu.Unlock()
			conn.Close()
		}()

		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				break
			}
		}
	}()
}

// Helper functions

func extractFrontmatterValue(content, key string) string {
	lines := strings.Split(content, "\n")
	inFrontmatter := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			if !inFrontmatter {
				inFrontmatter = true
				continue
			}
			break
		}
		if !inFrontmatter {
			continue
		}

		if strings.HasPrefix(trimmed, key+":") {
			value := strings.TrimSpace(strings.TrimPrefix(trimmed, key+":"))
			value = strings.Trim(value, "\"'")
			return value
		}
	}

	return ""
}

func extractImageBaseName(filename string) string {
	// Remove extension
	name := strings.TrimSuffix(filename, ".png")
	
	// Remove _header suffix and any numeric suffix
	// e.g., "turbocharging_header_1" -> "turbocharging"
	// e.g., "technology_header_5" -> "technology"
	
	// Split by underscore and find the base
	parts := strings.Split(name, "_")
	
	// Find where the pattern changes
	result := []string{}
	for i, part := range parts {
		// Skip if it's "header" or a number
		if part == "header" || part == "section" {
			break
		}
		// Check if it's a trailing number
		if i > 0 && isNumeric(part) {
			break
		}
		result = append(result, part)
	}

	if len(result) == 0 {
		return name
	}

	return strings.Join(result, "_")
}

func isNumeric(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(s) > 0
}

// ============================================================
// Logs
// ============================================================

type ResearcherLogsResponse struct {
	Path      string `json:"path"`
	Lines     int    `json:"lines"`
	Text      string `json:"text"`
	Truncated bool   `json:"truncated"`
}

func (s *Server) handleGetResearcherLogs(w http.ResponseWriter, r *http.Request) {
	lines := 300
	if v := r.URL.Query().Get("lines"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			if n > 5000 {
				n = 5000
			}
			lines = n
		}
	}

	logPath, tried := resolveResearcherLogPath()

	text, truncated, err := tailFileLines(logPath, lines)
	if err != nil {
		respondError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to read logs. Tried: %s. Error: %v", strings.Join(tried, ", "), err))
		return
	}

	respondJSON(w, http.StatusOK, ResearcherLogsResponse{
		Path:      logPath,
		Lines:     lines,
		Text:      text,
		Truncated: truncated,
	})
}

func resolveResearcherLogPath() (string, []string) {
	var tried []string

	// Respect explicit LOG_FILE first.
	if lf := os.Getenv("LOG_FILE"); lf != "" {
		// If relative, it resolves from current working directory.
		tried = append(tried, lf)
		if _, err := os.Stat(lf); err == nil {
			return lf, tried
		}
		// Also try relative to executable dir.
		if exe, err := os.Executable(); err == nil {
			p := filepath.Join(filepath.Dir(exe), lf)
			tried = append(tried, p)
			if _, err := os.Stat(p); err == nil {
				return p, tried
			}
		}
	}

	// Default filename.
	defaultName := "researcher.log"
	tried = append(tried, defaultName)
	if _, err := os.Stat(defaultName); err == nil {
		return defaultName, tried
	}

	// Try executable directory.
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), defaultName)
		tried = append(tried, p)
		if _, err := os.Stat(p); err == nil {
			return p, tried
		}
	}

	// Fall back to cwd path even if missing (tail will error with useful message)
	return defaultName, tried
}

// tailFileLines returns the last N lines of a file without reading it all.
func tailFileLines(path string, maxLines int) (string, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return "", false, err
	}
	if st.Size() == 0 {
		return "", false, nil
	}

	// Read from end in chunks, doubling until we have enough lines or hit a cap.
	var (
		chunkSize int64 = 256 * 1024 // 256KB
		maxBytes  int64 = 8 * 1024 * 1024
		data      []byte
	)
	for {
		if chunkSize > maxBytes {
			chunkSize = maxBytes
		}

		start := st.Size() - chunkSize
		if start < 0 {
			start = 0
		}
		buf := make([]byte, st.Size()-start)
		if _, err := f.ReadAt(buf, start); err != nil {
			// ReadAt can return EOF; ignore if we got data.
			if !strings.Contains(err.Error(), "EOF") {
				// On Windows, EOF is "EOF" too; fine.
			}
		}
		data = buf

		// Count lines quickly.
		count := 0
		for _, b := range data {
			if b == '\n' {
				count++
			}
		}
		if count >= maxLines+1 || start == 0 || chunkSize == maxBytes {
			break
		}
		chunkSize *= 2
	}

	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 {
		return "", false, nil
	}

	// Drop possible partial first line when we didn't start at 0.
	// Heuristic: if file is larger than data, first line likely partial.
	truncated := st.Size() > int64(len(data))
	if truncated && len(lines) > 0 {
		lines = lines[1:]
	}

	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}

	// Remove trailing empty line from split if present.
	if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}

	return strings.Join(lines, "\n"), truncated, nil
}

// ============================================================
// GitHub Issue & Branch Management Handlers
// ============================================================

// TopicIssue represents a research topic issue for the frontend
type TopicIssue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
}

// handleListTopicIssues returns all open "research topic" issues
func (s *Server) handleListTopicIssues(w http.ResponseWriter, r *http.Request) {
	if s.ghClient == nil {
		respondError(w, http.StatusServiceUnavailable, "GitHub client not initialized")
		return
	}

	issues, err := s.ghClient.GetTopicIssues()
	if err != nil {
		respondError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to fetch topic issues: %v", err))
		return
	}

	// Convert to simpler format for frontend
	var result []TopicIssue
	for _, issue := range issues {
		result = append(result, TopicIssue{
			Number: issue.GetNumber(),
			Title:  issue.GetTitle(),
			Body:   issue.GetBody(),
		})
	}

	respondJSON(w, http.StatusOK, result)
}

// handleGetIssue returns a specific issue by number
func (s *Server) handleGetIssue(w http.ResponseWriter, r *http.Request) {
	if s.ghClient == nil {
		respondError(w, http.StatusServiceUnavailable, "GitHub client not initialized")
		return
	}

	vars := mux.Vars(r)
	numberStr := vars["number"]
	number, err := strconv.Atoi(numberStr)
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid issue number")
		return
	}

	issue, err := s.ghClient.GetIssue(number)
	if err != nil {
		respondError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to fetch issue: %v", err))
		return
	}

	respondJSON(w, http.StatusOK, TopicIssue{
		Number: issue.GetNumber(),
		Title:  issue.GetTitle(),
		Body:   issue.GetBody(),
	})
}

// BranchIssueResponse contains the issue associated with the current branch
type BranchIssueResponse struct {
	IssueNumber int        `json:"issueNumber,omitempty"`
	Issue       *TopicIssue `json:"issue,omitempty"`
	BranchName  string     `json:"branchName"`
}

// handleGetBranchIssue returns the issue associated with the current branch
// Branch names are in the format: research/topic-{issueNumber}-{timestamp}
func (s *Server) handleGetBranchIssue(w http.ResponseWriter, r *http.Request) {
	branchInfo, err := s.gitMgr.GetBranchInfo()
	if err != nil {
		respondError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to get branch info: %v", err))
		return
	}

	response := BranchIssueResponse{
		BranchName: branchInfo.Name,
	}

	// Try to extract issue number from branch name
	// Format: research/topic-{number}-{timestamp}
	re := regexp.MustCompile(`research/topic-(\d+)-`)
	matches := re.FindStringSubmatch(branchInfo.Name)
	if len(matches) >= 2 {
		issueNumber, err := strconv.Atoi(matches[1])
		if err == nil {
			response.IssueNumber = issueNumber

			// Fetch the issue details if we have a GitHub client
			if s.ghClient != nil {
				issue, err := s.ghClient.GetIssue(issueNumber)
				if err == nil {
					response.Issue = &TopicIssue{
						Number: issue.GetNumber(),
						Title:  issue.GetTitle(),
						Body:   issue.GetBody(),
					}
				}
			}
		}
	}

	respondJSON(w, http.StatusOK, response)
}

// BranchInfo for listing branches
type BranchListItem struct {
	Name        string `json:"name"`
	IssueNumber int    `json:"issueNumber,omitempty"`
	IsResearch  bool   `json:"isResearch"`
}

// handleListBranches returns all branches, highlighting research branches
func (s *Server) handleListBranches(w http.ResponseWriter, r *http.Request) {
	// Get local branches using git command
	output, err := s.gitMgr.RunGit("branch", "--list")
	if err != nil {
		respondError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to list branches: %v", err))
		return
	}

	var branches []BranchListItem
	re := regexp.MustCompile(`research/topic-(\d+)-`)

	for _, line := range strings.Split(output, "\n") {
		name := strings.TrimSpace(strings.TrimPrefix(line, "*"))
		if name == "" {
			continue
		}

		branch := BranchListItem{
			Name: name,
		}

		// Check if it's a research branch and extract issue number
		matches := re.FindStringSubmatch(name)
		if len(matches) >= 2 {
			branch.IsResearch = true
			if num, err := strconv.Atoi(matches[1]); err == nil {
				branch.IssueNumber = num
			}
		}

		branches = append(branches, branch)
	}

	respondJSON(w, http.StatusOK, branches)
}

// DeleteBranchRequest for deleting a branch
type DeleteBranchRequest struct {
	RevertCheckboxes bool `json:"revertCheckboxes"`
}

// DeleteBranchResult for the response
type DeleteBranchResult struct {
	Success           bool     `json:"success"`
	Message           string   `json:"message"`
	RevertedArticles  []string `json:"revertedArticles,omitempty"`
	SwitchedToBranch  string   `json:"switchedToBranch"`
}

// handleDeleteBranch deletes the current branch, reverts checkboxes, and switches to main
func (s *Server) handleDeleteBranch(w http.ResponseWriter, r *http.Request) {
	branchInfo, err := s.gitMgr.GetBranchInfo()
	if err != nil {
		respondError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to get branch info: %v", err))
		return
	}

	if branchInfo.IsMain {
		respondError(w, http.StatusBadRequest, "Cannot delete main branch")
		return
	}

	var req DeleteBranchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// Default to reverting checkboxes
		req.RevertCheckboxes = true
	}

	result := DeleteBranchResult{
		RevertedArticles: []string{},
	}

	// Extract issue number from branch name
	re := regexp.MustCompile(`research/topic-(\d+)-`)
	matches := re.FindStringSubmatch(branchInfo.Name)
	
	// Revert checkboxes if requested and we have a GitHub client
	if req.RevertCheckboxes && len(matches) >= 2 && s.ghClient != nil {
		issueNumber, _ := strconv.Atoi(matches[1])
		
		// Get the issue body
		issue, err := s.ghClient.GetIssue(issueNumber)
		if err == nil {
			body := issue.GetBody()
			
			// Find all checked articles and uncheck them
			// We'll uncheck ALL checked items since the user wants to revert "all articles that were checked during this branch's lifetime"
			articles := gh.ParseArticlesFromBody(body)
			newBody := body
			
			for _, article := range articles {
				if article.Completed {
					// Uncheck this article
					newBody = uncheckArticleInBody(newBody, article.Name)
					result.RevertedArticles = append(result.RevertedArticles, article.Name)
				}
			}
			
			// Update the issue body if changes were made
			if len(result.RevertedArticles) > 0 {
				if err := s.ghClient.UpdateIssueBody(issueNumber, newBody); err != nil {
					log.Printf("[Delete Branch] Failed to update issue body: %v", err)
				}
			}
		}
	}

	branchName := branchInfo.Name

	// Switch to main branch first
	if err := s.gitMgr.CheckoutMain(); err != nil {
		respondError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to switch to main: %v", err))
		return
	}

	// Delete the local branch
	_, err = s.gitMgr.RunGit("branch", "-D", branchName)
	if err != nil {
		respondError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to delete branch: %v", err))
		return
	}

	// Also try to delete the remote branch (ignore errors if it doesn't exist)
	s.gitMgr.RunGit("push", "origin", "--delete", branchName)

	result.Success = true
	result.Message = fmt.Sprintf("Branch '%s' deleted successfully", branchName)
	result.SwitchedToBranch = "main"

	respondJSON(w, http.StatusOK, result)
}

// uncheckArticleInBody returns the issue body with the specified article unmarked
func uncheckArticleInBody(body, articleName string) string {
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		trimmedLine := strings.TrimSpace(line)
		// Match checked articles: "- [x] Article Name" or "- [X] Article Name"
		if strings.HasPrefix(trimmedLine, "- [x] ") || strings.HasPrefix(trimmedLine, "- [X] ") {
			currentArticle := trimmedLine[6:] // Remove "- [x] " prefix
			currentArticle = strings.TrimSpace(currentArticle)
			if currentArticle == articleName {
				// Preserve original indentation
				indent := line[:len(line)-len(trimmedLine)]
				lines[i] = indent + "- [ ] " + articleName
				break
			}
		}
	}
	return strings.Join(lines, "\n")
}

// SwitchBranchRequest for switching branches
type SwitchBranchRequest struct {
	Branch string `json:"branch"`
}

// handleSwitchBranch switches to a different branch
func (s *Server) handleSwitchBranch(w http.ResponseWriter, r *http.Request) {
	var req SwitchBranchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.Branch == "" {
		respondError(w, http.StatusBadRequest, "Branch name required")
		return
	}

	// Discard any local changes
	s.gitMgr.RunGit("reset", "--hard", "HEAD")
	s.gitMgr.RunGit("clean", "-fd")

	// Switch to the branch
	_, err := s.gitMgr.RunGit("checkout", req.Branch)
	if err != nil {
		respondError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to switch branch: %v", err))
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{
		"status": "switched",
		"branch": req.Branch,
	})
}

// CreateBranchRequest for creating a new branch
type CreateBranchRequest struct {
	IssueNumber int `json:"issueNumber"`
}

// CreateBranchResult for the response
type CreateBranchResult struct {
	Success    bool   `json:"success"`
	BranchName string `json:"branchName"`
	Message    string `json:"message"`
}

// handleCreateBranch creates a new branch from a topic issue
func (s *Server) handleCreateBranch(w http.ResponseWriter, r *http.Request) {
	var req CreateBranchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.IssueNumber <= 0 {
		respondError(w, http.StatusBadRequest, "Valid issue number required")
		return
	}

	// Generate branch name: research/topic-{issueNumber}-{timestamp}
	timestamp := time.Now().Format("20060102-150405")
	branchName := fmt.Sprintf("research/topic-%d-%s", req.IssueNumber, timestamp)

	// Make sure we're on main and up to date
	s.gitMgr.RunGit("checkout", "main")
	s.gitMgr.RunGit("pull", "origin", "main")

	// Create and checkout the new branch
	_, err := s.gitMgr.RunGit("checkout", "-b", branchName)
	if err != nil {
		respondError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to create branch: %v", err))
		return
	}

	// Push the new branch to origin
	_, err = s.gitMgr.RunGit("push", "-u", "origin", branchName)
	if err != nil {
		// Not critical, branch was created locally
		log.Printf("[Create Branch] Failed to push branch to origin: %v", err)
	}

	respondJSON(w, http.StatusOK, CreateBranchResult{
		Success:    true,
		BranchName: branchName,
		Message:    fmt.Sprintf("Created and switched to branch '%s'", branchName),
	})
}
