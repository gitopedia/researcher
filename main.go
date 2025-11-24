package main

import (
	"context"
	"log"

	"github.com/gitopedia/researcher/internal/agent"
	"github.com/joho/godotenv"
)

func main() {
	// Load .env file if it exists
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, relying on environment variables")
	}

	log.Println("Starting Researcher Agent (Go)...")

	ctx := context.Background()
	a, err := agent.NewAgent(ctx)
	if err != nil {
		log.Fatalf("Failed to initialize agent: %v", err)
	}

	if err := a.Run(ctx); err != nil {
		log.Fatalf("Agent run failed: %v", err)
	}
}
