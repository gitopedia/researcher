package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/gitopedia/researcher/internal/agent"
	"github.com/gitopedia/researcher/internal/logging"
	"github.com/joho/godotenv"
)

func main() {
	// Parse CLI flags
	mergeOnly := flag.Bool("merge-only", false, "Only run the PR merge logic, don't process new issues")
	once := flag.Bool("once", false, "Run once and exit (no loop)")
	stepByStep := flag.Bool("step", false, "Run in step-by-step mode, pausing for manual triggers")
	stepName := flag.String("step-name", "", "Specific step to run (discovery, summarization, drafting, finalize)")
	repoPath := flag.String("repo-path", "../gitopedia", "Path to local gitopedia repository")
	noCommit := flag.Bool("no-commit", false, "Add changes to staging area but don't commit")
	generateImages := flag.Bool("generate-images", false, "Run image generation only for pending prompts on current branch")
	backfillImages := flag.Bool("backfill-images", false, "Generate prompts for existing articles that don't have them, then generate images")
	branchName := flag.String("branch", "", "Branch name for image operations (required with --generate-images or --backfill-images)")
	flag.Parse()

	// Initialize structured, colorized logging using Go's standard library slog,
	// and route the standard log package through it.
	logging.Init()

	// Load config/base.env first (base configuration), then config/.env (user overrides)
	// config/base.env contains all default settings; config/.env only needs to override specific values
	if err := godotenv.Load("config/base.env"); err != nil {
		log.Println("config/base.env not found, using defaults and environment variables")
	}
	// config/.env overrides values from config/base.env (Overload forces override even if variable already exists)
	if err := godotenv.Overload("config/.env"); err != nil {
		log.Println("config/.env not found, using config/base.env defaults only")
	}

	log.Printf("Gitopedia Researcher v%s", agent.Version)
	log.Printf("Repository: %s", *repoPath)
	if *backfillImages {
		log.Println("Starting in backfill images mode (generate prompts + images for existing articles)...")
	} else if *generateImages {
		log.Println("Starting in image generation mode...")
	} else if *mergeOnly {
		log.Println("Starting in merge-only mode...")
	} else if *stepByStep {
		log.Printf("Starting in step-by-step mode (Step: %s)...", *stepName)
	} else {
		log.Println("Starting in full mode...")
	}
	if !*once && !*generateImages {
		log.Println("Press Ctrl+C to gracefully shutdown (will wait for current task to complete)")
	}

	// Create a context that can be cancelled with signals (Ctrl+C)
	ctx, cancel := context.WithCancel(context.Background())

	// Track if we're currently running a task
	var taskMu sync.Mutex
	taskRunning := false
	shutdownRequested := false

	// Handle SIGINT (Ctrl+C) and SIGTERM
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig := <-sigChan
		taskMu.Lock()
		shutdownRequested = true
		running := taskRunning
		taskMu.Unlock()

		if running {
			log.Printf("Received %v - cancelling after current LLM inference completes...", sig)
			log.Println("(Press Ctrl+C again to force quit immediately)")
			// Cancel context so operations see the shutdown request
			cancel()

			// Wait for second signal to force quit
			sig = <-sigChan
			log.Printf("Received %v again - forcing immediate shutdown", sig)
			os.Exit(130)
		} else {
			log.Printf("Received %v - shutting down", sig)
			cancel()
		}
	}()

	a, err := agent.NewAgent(ctx, *repoPath)
	if err != nil {
		log.Fatalf("Failed to initialize agent: %v", err)
	}

	if *noCommit {
		a.SetNoCommit(true)
	}

	// Handle backfill images mode (generate prompts for existing articles, then images)
	if *backfillImages {
		if *branchName == "" {
			log.Fatal("--branch is required when using --backfill-images")
		}
		log.Printf("Backfilling images for branch: %s", *branchName)
		if err := a.BackfillImagePrompts(ctx, *branchName); err != nil {
			log.Fatalf("Backfill image prompts failed: %v", err)
		}
		log.Println("Backfill completed, now generating images...")
		if err := a.GenerateImagesForBranch(ctx, *branchName); err != nil {
			log.Fatalf("Image generation failed: %v", err)
		}
		log.Println("Backfill and image generation completed successfully")
		logging.Close()
		return
	}

	// Handle image generation mode
	if *generateImages {
		if *branchName == "" {
			log.Fatal("--branch is required when using --generate-images")
		}
		log.Printf("Generating images for branch: %s", *branchName)
		if err := a.GenerateImagesForBranch(ctx, *branchName); err != nil {
			log.Fatalf("Image generation failed: %v", err)
		}
		log.Println("Image generation completed successfully")
		logging.Close()
		return
	}

	// Loop interval configuration
	loopInterval := 60 * time.Second
	if envVal := os.Getenv("LOOP_INTERVAL_SECONDS"); envVal != "" {
		if v, err := strconv.Atoi(envVal); err == nil && v > 0 {
			loopInterval = time.Duration(v) * time.Second
		}
	}

	// Main loop
	for {
		// Check if shutdown was requested
		taskMu.Lock()
		if shutdownRequested {
			taskMu.Unlock()
			log.Println("Shutdown requested, exiting loop")
			break
		}
		taskRunning = true
		taskMu.Unlock()

		// Run one iteration
		var err error
		if *mergeOnly {
			err = a.MergeOnly(ctx)
		} else {
			err = a.Run(ctx, *stepByStep, *stepName)
		}

		taskMu.Lock()
		taskRunning = false
		shouldExit := shutdownRequested
		taskMu.Unlock()

		if err != nil {
			if err == context.Canceled {
				log.Println("Agent run cancelled by user")
				break
			}
			log.Printf("Agent run error: %v", err)
			// Continue to next iteration after error
		}

		if shouldExit {
			log.Println("Shutdown requested, exiting after task completion")
			break
		}

		// Exit after one run if --once flag is set
		if *once {
			log.Println("Single run completed (--once mode)")
			break
		}

		log.Printf("Sleeping for %v before next run...", loopInterval)

		// Sleep with cancellation check
		select {
		case <-ctx.Done():
			log.Println("Context cancelled during sleep")
			return
		case <-time.After(loopInterval):
			// Continue to next iteration
		}

		// Check context after sleep
		if ctx.Err() != nil {
			break
		}
	}

	log.Println("Researcher Agent stopped gracefully")
	logging.Close()
}
