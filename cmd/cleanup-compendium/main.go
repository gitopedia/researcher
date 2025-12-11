package main

import (
	"context"
	"log"
	"strings"

	"github.com/gitopedia/researcher/internal/github"
	"github.com/joho/godotenv"
)

func main() {
	// Load config from project root (same as main.go)
	if err := godotenv.Load("config/base.env"); err != nil {
		log.Println("config/base.env not found, using defaults and environment variables")
	}
	if err := godotenv.Overload("config/.env"); err != nil {
		log.Println("config/.env not found, using config/base.env defaults only")
	}

	ctx := context.Background()
	ghClient, err := github.NewClient(ctx)
	if err != nil {
		log.Fatalf("Failed to create GitHub client: %v", err)
	}

	// List all files in Compendium
	log.Println("Listing files in Compendium...")
	files, err := ghClient.ListAllFiles("Compendium")
	if err != nil {
		log.Fatalf("Failed to list files: %v", err)
	}

	log.Printf("Found %d files in Compendium", len(files))

	// Filter out index.md files (keep those)
	var filesToDelete []string
	for _, f := range files {
		// Keep index.md files
		if strings.HasSuffix(f, "index.md") {
			log.Printf("Keeping: %s", f)
			continue
		}
		filesToDelete = append(filesToDelete, f)
	}

	log.Printf("Will delete %d files (keeping %d index.md files)", len(filesToDelete), len(files)-len(filesToDelete))

	// Create a branch for cleanup
	branchName := "cleanup/compendium-cleanup"
	log.Printf("Creating branch: %s", branchName)
	if err := ghClient.CreateBranch("main", branchName); err != nil {
		if strings.Contains(err.Error(), "already exists") {
			log.Printf("Branch already exists, will continue on existing branch")
		} else {
			log.Fatalf("Failed to create branch: %v", err)
		}
	}

	// Delete files
	deleted := 0
	failed := 0
	skipped := 0
	total := len(filesToDelete)

	for i, filePath := range filesToDelete {
		// Show progress every 50 files
		if i%50 == 0 && i > 0 {
			log.Printf("Progress: %d/%d (%.1f%%) - Deleted: %d, Failed: %d, Skipped: %d",
				i, total, float64(i)/float64(total)*100, deleted, failed, skipped)
		}

		// Check if file exists in main (if not, skip)
		_, _, err := ghClient.GetFile("main", filePath)
		if err != nil {
			// File doesn't exist in main, skip
			skipped++
			continue
		}

		// Get file SHA from the branch we're deleting from
		_, sha, err := ghClient.GetFile(branchName, filePath)
		if err != nil {
			// File doesn't exist in branch (already deleted), skip
			skipped++
			continue
		}

		if err := ghClient.DeleteFile(branchName, filePath, "Clean up Compendium: remove old workflow artifacts", sha); err != nil {
			if strings.Contains(err.Error(), "does not exist") || strings.Contains(err.Error(), "not found") {
				skipped++
			} else {
				log.Printf("Failed to delete %s: %v", filePath, err)
				failed++
			}
		} else {
			deleted++
		}
	}

	log.Printf("Cleanup complete! Deleted: %d, Failed: %d", deleted, failed)
	log.Printf("Branch created: %s - Review and merge when ready", branchName)
}
