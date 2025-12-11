package main

import (
	"context"
	"log"
	"strings"

	"github.com/gitopedia/researcher/internal/github"
	"github.com/joho/godotenv"
)

func main() {
	// Load config
	if err := godotenv.Load("config/base.env"); err != nil {
		log.Println("config/base.env not found, using defaults")
	}
	if err := godotenv.Overload("config/.env"); err != nil {
		log.Println("config/.env not found")
	}

	ctx := context.Background()
	ghClient, err := github.NewClient(ctx)
	if err != nil {
		log.Fatalf("Failed to create GitHub client: %v", err)
	}

	// Create a fresh branch
	branchName := "cleanup/compendium-round2"
	log.Printf("Creating branch: %s", branchName)
	if err := ghClient.CreateBranch("main", branchName); err != nil {
		if strings.Contains(err.Error(), "already exists") {
			log.Printf("Branch exists, using existing branch")
		} else {
			log.Fatalf("Failed to create branch: %v", err)
		}
	}

	// Focus on _debug and _incoming directories
	log.Println("Listing files in Compendium/_debug and Compendium/_incoming...")
	allFiles, err := ghClient.ListAllFiles("Compendium")
	if err != nil {
		log.Fatalf("Failed to list files: %v", err)
	}

	var filesToDelete []string
	for _, f := range allFiles {
		// Delete everything in _debug and _incoming
		if strings.HasPrefix(f, "Compendium/_debug/") || strings.HasPrefix(f, "Compendium/_incoming/") {
			// Skip index.md files
			if !strings.HasSuffix(f, "index.md") {
				filesToDelete = append(filesToDelete, f)
			}
		}
		// Also delete all article .md files (not index.md)
		if strings.HasSuffix(f, ".md") && !strings.HasSuffix(f, "index.md") {
			if !strings.HasPrefix(f, "Compendium/_debug/") && !strings.HasPrefix(f, "Compendium/_incoming/") {
				filesToDelete = append(filesToDelete, f)
			}
		}
	}

	log.Printf("Will delete %d files", len(filesToDelete))

	// Delete files
	deleted := 0
	failed := 0
	skipped := 0
	total := len(filesToDelete)

	for i, filePath := range filesToDelete {
		if i%100 == 0 && i > 0 {
			log.Printf("Progress: %d/%d (%.1f%%) - Deleted: %d, Failed: %d, Skipped: %d",
				i, total, float64(i)/float64(total)*100, deleted, failed, skipped)
		}

		// Get file SHA from main
		_, sha, err := ghClient.GetFile("main", filePath)
		if err != nil {
			skipped++
			continue
		}

		// Delete from branch
		if err := ghClient.DeleteFile(branchName, filePath, "Clean up Compendium: remove old workflow artifacts and articles", sha); err != nil {
			if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "not found") {
				skipped++
			} else {
				log.Printf("Failed to delete %s: %v", filePath, err)
				failed++
			}
		} else {
			deleted++
		}
	}

	log.Printf("\n=== Cleanup Complete ===")
	log.Printf("Deleted: %d, Failed: %d, Skipped: %d", deleted, failed, skipped)
	log.Printf("Branch: %s - Review and merge when ready", branchName)
}
