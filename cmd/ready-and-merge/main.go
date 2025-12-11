package main

import (
	"context"
	"log"

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

	prNumber := 114
	log.Printf("Marking PR #%d as ready for review...", prNumber)
	if err := ghClient.MarkPRReady(prNumber); err != nil {
		log.Fatalf("Failed to mark PR ready: %v", err)
	}

	log.Printf("Merging PR #%d...", prNumber)
	if err := ghClient.MergePR(prNumber, "Clean up Compendium: Remove old workflow artifacts"); err != nil {
		log.Fatalf("Failed to merge PR: %v", err)
	}

	log.Printf("Successfully merged PR #%d! Compendium is now clean.", prNumber)
}
