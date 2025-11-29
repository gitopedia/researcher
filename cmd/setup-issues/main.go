package main

import (
	"context"
	"log"

	"github.com/gitopedia/researcher/internal/github"
	"github.com/gitopedia/researcher/internal/logging"
	"github.com/joho/godotenv"
)

var categories = []string{
	"Computer Science",
	"Physics",
	"Biology",
	"World History",
	"Philosophy",
	"Economics",
	"Space Exploration",
	"Medical Science",
	"Sustainable Energy",
	"Artificial Intelligence",
}

func main() {
	// Initialize structured, colorized logging for this command as well.
	logging.Init()

	// Load config/base.env first (base configuration), then .env (user overrides)
	if err := godotenv.Load("config/base.env"); err != nil {
		log.Println("config/base.env not found, using defaults and environment variables")
	}
	// .env overrides values from config/base.env (Overload forces override even if variable already exists)
	if err := godotenv.Overload(".env"); err != nil {
		log.Println(".env not found, using config/base.env defaults only")
	}

	ctx := context.Background()
	client, err := github.NewClient(ctx)
	if err != nil {
		log.Fatalf("Failed to create GitHub client: %v", err)
	}

	// Check existing issues
	// Since client.GetResearchRequests only gets open ones with label 'research category',
	// we should probably list ALL issues to avoid duplicates if they are closed or have different labels?
	// Or just trust the label.
	// But the client doesn't expose ListAllIssues.
	// For this script, we'll assume if we don't see it in GetResearchRequests, we create it.
	// Better: we should check if an issue with the title exists.

	existing, err := client.GetResearchRequests()
	if err != nil {
		log.Fatalf("Failed to get existing requests: %v", err)
	}

	existingTitles := make(map[string]bool)
	for _, iss := range existing {
		existingTitles[*iss.Title] = true
	}

	for _, cat := range categories {
		title := "Category: " + cat
		if existingTitles[title] {
			log.Printf("Issue '%s' already exists. Skipping.", title)
			continue
		}

		log.Printf("Creating issue '%s'...", title)
		body := "Tracking issue for expanding the category: " + cat
		labels := []string{"research category"}

		if _, err := client.CreateIssue(title, body, labels); err != nil {
			log.Printf("Failed to create issue '%s': %v", title, err)
		} else {
			log.Printf("Created issue '%s'", title)
		}
	}
}
