package main

import (
	"context"
	"flag"
	"fmt"
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
	var (
		closeAllIssues = flag.Bool("close-issues", false, "Close all open issues")
		closeAllPRs    = flag.Bool("close-prs", false, "Close all open pull requests")
		deleteBranches = flag.Bool("delete-branches", false, "Delete all branches except main/master")
		dryRun         = flag.Bool("dry-run", false, "Show what would be closed/deleted without actually doing it")
		confirm        = flag.Bool("confirm", false, "Confirm deletion (required for actual cleanup)")
	)
	flag.Parse()

	if !*dryRun && !*confirm {
		log.Fatal("Error: --confirm flag is required for actual cleanup. Use --dry-run to preview changes.")
	}

	ctx := context.Background()
	client, err := github.NewClient(ctx)
	if err != nil {
		log.Fatalf("Failed to create GitHub client: %v", err)
	}

	if *closeAllIssues {
		if err := cleanupIssues(client, *dryRun); err != nil {
			log.Fatalf("Failed to cleanup issues: %v", err)
		}
	}

	if *closeAllPRs {
		if err := cleanupPRs(client, *dryRun); err != nil {
			log.Fatalf("Failed to cleanup PRs: %v", err)
		}
	}

	if *deleteBranches {
		if err := cleanupBranches(client, *dryRun); err != nil {
			log.Fatalf("Failed to cleanup branches: %v", err)
		}
	}

	if !*closeAllIssues && !*closeAllPRs && !*deleteBranches {
		log.Println("No action specified. Use --close-issues, --close-prs, and/or --delete-branches")
		log.Println("Use --dry-run to preview what would be closed/deleted")
	}
}

func cleanupIssues(client *github.Client, dryRun bool) error {
	log.Println("Fetching all open issues...")

	issues, err := client.ListAllOpenIssues()
	if err != nil {
		return fmt.Errorf("failed to get issues: %w", err)
	}

	log.Printf("Found %d open issues", len(issues))

	if len(issues) == 0 {
		log.Println("No open issues to close")
		return nil
	}

	for _, issue := range issues {
		// Skip PRs (they're also issues in GitHub's API)
		if issue.PullRequestLinks != nil {
			continue
		}

		log.Printf("Issue #%d: %s", issue.GetNumber(), issue.GetTitle())
		if !dryRun {
			if err := client.CloseIssue(issue.GetNumber()); err != nil {
				log.Printf("  Error closing issue #%d: %v", issue.GetNumber(), err)
				continue
			}
			log.Printf("  ✓ Closed issue #%d", issue.GetNumber())
		} else {
			log.Printf("  [DRY RUN] Would close issue #%d", issue.GetNumber())
		}
	}

	return nil
}

func cleanupPRs(client *github.Client, dryRun bool) error {
	log.Println("Fetching all open pull requests...")

	prs, err := client.ListOpenPRs()
	if err != nil {
		return fmt.Errorf("failed to list PRs: %w", err)
	}

	log.Printf("Found %d open pull requests", len(prs))

	if len(prs) == 0 {
		log.Println("No open PRs to close")
		return nil
	}

	for _, pr := range prs {
		log.Printf("PR #%d: %s (Draft: %v, Branch: %s)", pr.Number, pr.Title, pr.Draft, pr.HeadBranch)
		if !dryRun {
			if err := client.ClosePR(pr.Number); err != nil {
				log.Printf("  Error closing PR #%d: %v", pr.Number, err)
				continue
			}
			log.Printf("  ✓ Closed PR #%d", pr.Number)
		} else {
			log.Printf("  [DRY RUN] Would close PR #%d", pr.Number)
		}
	}

	return nil
}

func cleanupBranches(client *github.Client, dryRun bool) error {
	log.Println("Fetching all branches...")

	branches, err := client.ListBranches()
	if err != nil {
		return fmt.Errorf("failed to list branches: %w", err)
	}

	log.Printf("Found %d branches", len(branches))

	if len(branches) == 0 {
		log.Println("No branches to delete")
		return nil
	}

	protectedBranches := map[string]bool{
		"main":   true,
		"master": true,
	}

	deletedCount := 0
	for _, branch := range branches {
		branchName := branch.GetName()
		if protectedBranches[branchName] {
			log.Printf("Branch '%s': Protected (skipping)", branchName)
			continue
		}

		log.Printf("Branch '%s'", branchName)
		if !dryRun {
			if err := client.DeleteBranch(branchName); err != nil {
				log.Printf("  Error deleting branch '%s': %v", branchName, err)
				continue
			}
			log.Printf("  ✓ Deleted branch '%s'", branchName)
			deletedCount++
		} else {
			log.Printf("  [DRY RUN] Would delete branch '%s'", branchName)
			deletedCount++
		}
	}

	if !dryRun {
		log.Printf("Deleted %d branches", deletedCount)
	} else {
		log.Printf("Would delete %d branches", deletedCount)
	}

	return nil
}

