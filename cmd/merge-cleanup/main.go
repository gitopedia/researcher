package main

import (
	"context"
	"log"
	"os"

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

	// Check for cleanup PR
	prs, err := ghClient.ListOpenPRs()
	if err != nil {
		log.Fatalf("Failed to list PRs: %v", err)
	}

	var cleanupPR *github.PRInfo
	for _, pr := range prs {
		if pr.HeadBranch == "cleanup/compendium-fresh" ||
			pr.HeadBranch == "cleanup/compendium-cleanup" {
			cleanupPR = pr
			break
		}
	}

	if cleanupPR == nil {
		log.Println("No cleanup PR found. Creating one for cleanup/compendium-fresh...")
		pr, err := ghClient.CreatePullRequest(
			"Clean up Compendium: Remove old workflow artifacts",
			"Removes all files from _debug, _incoming, and all article files to start fresh with new incremental workflow.",
			"cleanup/compendium-fresh",
			"main",
		)
		if err != nil {
			log.Fatalf("Failed to create PR: %v", err)
		}
		cleanupPR = &github.PRInfo{
			Number: *pr.Number,
			Title:  pr.GetTitle(),
		}
		log.Printf("Created PR #%d", cleanupPR.Number)
	}

	log.Printf("Found cleanup PR #%d: %s", cleanupPR.Number, cleanupPR.Title)

	// Check if we should merge
	if len(os.Args) > 1 && os.Args[1] == "merge" {
		log.Printf("Merging PR #%d...", cleanupPR.Number)
		if err := ghClient.MergePR(cleanupPR.Number, "Clean up Compendium: Remove old workflow artifacts"); err != nil {
			log.Fatalf("Failed to merge PR: %v", err)
		}
		log.Printf("Successfully merged PR #%d", cleanupPR.Number)
	} else {
		log.Printf("PR #%d is ready. To merge, run: go run ./cmd/merge-cleanup/main.go merge", cleanupPR.Number)
		log.Printf("Or merge manually via GitHub UI")
	}
}
