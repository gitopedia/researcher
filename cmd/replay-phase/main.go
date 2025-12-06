package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/gitopedia/researcher/internal/agent"
	"github.com/gitopedia/researcher/internal/llm"
	"github.com/gitopedia/researcher/internal/logging"
	"github.com/joho/godotenv"
)

func main() {
	// Parse CLI flags
	localPath := flag.String("local-path", "", "Path to local debug artifacts (e.g. Compendium/_debug/articles/slug)")
	phase := flag.Int("phase", 6, "Phase to replay (currently only 6 is supported)")
	topic := flag.String("topic", "", "Topic name (required)")
	flag.Parse()

	if *localPath == "" || *topic == "" {
		log.Fatal("Usage: replay-phase --local-path <path> --topic <topic> [--phase <6>]")
	}

	// Initialize logging
	logging.Init()

	// Load environment
	if err := godotenv.Load("../../config/base.env"); err != nil {
		log.Println("Warning: config/base.env not found, trying .env")
		godotenv.Load(".env")
	}

	// Create agent deps (mocks or real)
	// For replay, we need real LLM but mock/noop GitHub/Search if possible
	// But we can just use the real agent constructor and ignore GH/Search calls if we're careful
	ctx := context.Background()
	
	// Initialize LLM client
	llmClient, err := llm.NewClient()
	if err != nil {
		log.Fatalf("Failed to create LLM client: %v", err)
	}

	// Create a minimal agent with dependencies
	// We'll use nil for GitHub and Search since we're loading from local files for this replay
	a := agent.NewAgentWithDeps(nil, nil, llmClient)

	if *phase == 6 {
		if err := replayPhase6(ctx, a, *topic, *localPath); err != nil {
			log.Fatalf("Replay failed: %v", err)
		}
	} else {
		log.Fatalf("Phase %d replay not implemented yet", *phase)
	}
}

func replayPhase6(ctx context.Context, a *agent.Agent, topic, debugPath string) error {
	log.Printf("Replaying Phase 6 (Integration) for '%s' using artifacts from %s", topic, debugPath)

	// 1. Load outline to get structure
	outlinePath := filepath.Join(debugPath, "outline.json")
	outlineData, err := os.ReadFile(outlinePath)
	if err != nil {
		return fmt.Errorf("failed to read outline.json: %w", err)
	}

	var outline agent.ArticleOutline
	if err := json.Unmarshal(outlineData, &outline); err != nil {
		return fmt.Errorf("failed to parse outline.json: %w", err)
	}

	// 2. Load Phase 4 sections and populate outline content
	sectionsDir := filepath.Join(debugPath, "phase4_sections")
	files, err := os.ReadDir(sectionsDir)
	if err != nil {
		return fmt.Errorf("failed to list phase 4 sections: %w", err)
	}

	// Map filenames to content
	sectionContent := make(map[string]string)
	for _, f := range files {
		if filepath.Ext(f.Name()) != ".md" {
			continue
		}
		content, err := os.ReadFile(filepath.Join(sectionsDir, f.Name()))
		if err != nil {
			log.Printf("Warning: failed to read %s: %v", f.Name(), err)
			continue
		}
		sectionContent[f.Name()] = string(content)
	}

	// Populate outline sections with loaded content
	// Filename format: section-N-slug.md or section-N.M-slug.md
	slug := strings.ToLower(strings.ReplaceAll(topic, " ", "-")) // Using inline slugify
	for i := range outline.Sections {
		sec := &outline.Sections[i]
		// Try to find matching file
		fname := fmt.Sprintf("section-%d-%s.md", i+1, slug)
		if content, ok := sectionContent[fname]; ok {
			// Strip the heading from the file content since we just want the body for the struct
			// The file content is "## Heading\n\nBody..."
			parts := strings.SplitN(content, "\n\n", 2)
			if len(parts) > 1 {
				sec.Content = parts[1]
			} else {
				sec.Content = content
			}
		} else {
			log.Printf("Warning: missing content for section %d: %s", i+1, sec.Heading)
		}

		for j := range sec.Subsections {
			sub := &sec.Subsections[j]
			fname := fmt.Sprintf("section-%d.%d-%s.md", i+1, j+1, slug)
			if content, ok := sectionContent[fname]; ok {
				parts := strings.SplitN(content, "\n\n", 2)
				if len(parts) > 1 {
					sub.Content = parts[1]
				} else {
					sub.Content = content
				}
			}
		}
	}

	// 3. Check for Phase 5 discovery and append to outline if needed
	discoveryPath := filepath.Join(debugPath, "phase5_discovery.json")
	if data, err := os.ReadFile(discoveryPath); err == nil {
		// This JSON structure in debug artifact is map[string]interface{}, need to parse specifically
		// Or we can just look for section files that don't match the original outline indices?
		// For simplicity in this v1 replay tool, we'll rely on the section files if the outline was updated
		// But actually, outline.json might be the *initial* outline.
		// The debug artifact for Phase 5 contains the updated outline? No, it contains the discovery result.
		// Let's rely on the fact that agent.go appends discovered sections to the outline object in memory.
		// For replay, if we want to be exact, we should load phase5_discovery.json and append those sections.
		
		// Parse the debug wrapper
		var debugWrapper struct {
			Discovery agent.SectionDiscovery `json:"discovery"`
		}
		if err := json.Unmarshal(data, &debugWrapper); err == nil && len(debugWrapper.Discovery.SuggestedSections) > 0 {
			log.Printf("Found %d discovered sections in Phase 5 artifact", len(debugWrapper.Discovery.SuggestedSections))
			// TODO: We need to populate these into outline.Sections if they aren't there
			// But simpler: just look for section files starting with index > initial outline length
			// The loop above iterated outline.Sections. If outline.json is from Phase 1, it won't have Phase 5 sections.
			
			// Let's try to reconstruct extra sections from files
			// Current outline has N sections. Look for section-(N+1)...
			startIdx := len(outline.Sections) + 1
			for {
				fname := fmt.Sprintf("section-%d-%s.md", startIdx, slug)
				content, ok := sectionContent[fname]
				if !ok {
					break
				}
				
				// Parse heading from content "## Heading"
				lines := strings.Split(content, "\n")
				heading := strings.TrimPrefix(lines[0], "## ")
				body := ""
				if len(lines) > 1 {
					body = strings.Join(lines[1:], "\n")
				}

				newSec := agent.SectionOutline{
					Heading: heading,
					Level: 2,
					Content: strings.TrimSpace(body),
				}
				outline.Sections = append(outline.Sections, newSec)
				log.Printf("Added discovered section from file: %s", heading)
				startIdx++
			}
		}
	}

	// 4. Run Integration
	log.Println("Starting Phase 6 Integration...")
	result, err := a.Phase6IntegrateArticle(ctx, topic, &outline)
	if err != nil {
		return fmt.Errorf("integration failed: %w", err)
	}

	// 5. Save output
	outputPath := filepath.Join(debugPath, "phase6_integrated_replay.md")
	if err := os.WriteFile(outputPath, []byte(result), 0644); err != nil {
		return fmt.Errorf("failed to save output: %w", err)
	}

	fmt.Printf("\nReplay Complete!\nSaved to: %s\nOutput length: %d words\n", outputPath, countWords(result))
	return nil
}

func countWords(s string) int {
	return len(strings.Fields(s))
}
