package main

import (
	"context"
	"log"
	"os"

	"github.com/google/go-github/v57/github"
	"github.com/joho/godotenv"
	"golang.org/x/oauth2"
)

func main() {
	// Load .env file
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, relying on environment variables.")
	}

	ctx := context.Background()
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		log.Fatalf("GITHUB_TOKEN environment variable not set")
	}

	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)

	owner := "gitopedia"
	repo := "gitopedia"

	// List all open issues
	opts := &github.IssueListByRepoOptions{
		State: "open",
		ListOptions: github.ListOptions{PerPage: 100},
	}

	allIssues, _, err := client.Issues.ListByRepo(ctx, owner, repo, opts)
	if err != nil {
		log.Fatalf("Failed to list issues: %v", err)
	}

	// Find issues with the old label
	updated := 0
	for _, issue := range allIssues {
		hasOldLabel := false
		for _, label := range issue.Labels {
			if label.GetName() == "research-category" {
				hasOldLabel = true
				break
			}
		}

		if hasOldLabel {
			// Remove old label and add new label
			labels := []string{}
			for _, label := range issue.Labels {
				if label.GetName() != "research-category" {
					labels = append(labels, label.GetName())
				}
			}
			labels = append(labels, "Research Category")

			_, _, err := client.Issues.Edit(ctx, owner, repo, issue.GetNumber(), &github.IssueRequest{
				Labels: &labels,
			})
			if err != nil {
				log.Printf("Failed to update issue #%d: %v", issue.GetNumber(), err)
			} else {
				log.Printf("Updated issue #%d: %s", issue.GetNumber(), issue.GetTitle())
				updated++
			}
		}
	}

	log.Printf("Updated %d issue(s)", updated)
}
