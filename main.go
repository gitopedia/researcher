package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/gitopedia/researcher/internal/agent"
	"github.com/joho/godotenv"
)

func main() {
	// Load config/base.env first (base configuration), then .env (user overrides)
	// config/base.env contains all default settings; .env only needs to override specific values
	if err := godotenv.Load("config/base.env"); err != nil {
		log.Println("config/base.env not found, using defaults and environment variables")
	}
	// .env overrides values from config/base.env (Overload forces override even if variable already exists)
	if err := godotenv.Overload(".env"); err != nil {
		log.Println(".env not found, using config/base.env defaults only")
	}

	log.Println("Starting Researcher Agent (Go)...")

	// Create a context that can be cancelled with signals (Ctrl+C)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle SIGINT (Ctrl+C) and SIGTERM
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig := <-sigChan
		log.Printf("Received signal: %v, cancelling...", sig)
		cancel()
	}()

	a, err := agent.NewAgent(ctx)
	if err != nil {
		log.Fatalf("Failed to initialize agent: %v", err)
	}

	if err := a.Run(ctx); err != nil {
		if err == context.Canceled {
			log.Println("Agent run cancelled by user")
			os.Exit(130) // Standard exit code for SIGINT
		} else {
			log.Fatalf("Agent run failed: %v", err)
		}
	}
}
