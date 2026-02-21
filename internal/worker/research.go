package worker

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/gitopedia/researcher/internal/agent"
	"github.com/gitopedia/researcher/internal/llm"
	"github.com/gitopedia/researcher/internal/queue"
	"github.com/gitopedia/researcher/internal/repository"
	"github.com/gitopedia/researcher/internal/search"
)

// ResearchWorker continuously builds and improves articles on a branch until
// stopped. It delegates to the existing agent.Agent business logic but routes
// all LLM calls through the queue.
type ResearchWorker struct {
	Base

	// Factory deps – used to construct an Agent per run.
	repoMgr  repository.RepoManager
	searcher search.Searcher
	llmGen   llm.Generator // raw client (will be wrapped in queue)
}

// NewResearchWorker creates a research worker.
func NewResearchWorker(cfg Config, qm *queue.Manager, logger *slog.Logger, repoMgr repository.RepoManager, searcher search.Searcher, llmGen llm.Generator) *ResearchWorker {
	return &ResearchWorker{
		Base:     NewBase(cfg, qm, logger),
		repoMgr:  repoMgr,
		searcher: searcher,
		llmGen:   llmGen,
	}
}

// Start begins the continuous research loop. It blocks until the context is
// cancelled or Stop() is called.
func (w *ResearchWorker) Start(ctx context.Context) error {
	ctx, w.cancel = context.WithCancel(ctx)
	defer func() {
		w.SetState(StateStopped)
		w.SetStep("")
	}()

	w.mu.Lock()
	w.startedAt = time.Now()
	w.iterations = 0
	w.errors = 0
	w.lastError = ""
	w.mu.Unlock()
	w.SetState(StateStarting)

	branch := w.Branch()
	if branch == "" {
		w.SetState(StateError)
		return fmt.Errorf("no branch configured")
	}

	w.Logger().Info("Starting research worker", "branch", branch)

	// Create a queue-wrapped LLM client so all calls are serialised and retried.
	queuedLLM := queue.NewQueuedLLMClient(w.llmGen, w.Queue().LLM)

	// Build an Agent that uses the queued LLM client.
	ag := agent.NewAgentWithDeps(w.repoMgr, w.searcher, queuedLLM)
	ag.SetNoCommit(false) // workers commit their changes

	w.SetState(StateRunning)

	// Continuous loop: keep improving articles until stopped.
	for {
		if err := w.WaitIfPaused(ctx); err != nil {
			return err
		}

		select {
		case <-ctx.Done():
			w.Logger().Info("Research worker stopped", "branch", branch, "iterations", w.Status().Iterations)
			return nil
		default:
		}

		w.SetStep("Running research iteration")
		w.Logger().Info("Starting iteration", "iteration", w.Status().Iterations+1)

		// Run one pass of the agent's research logic.
		if err := ag.Run(ctx); err != nil {
			if ctx.Err() != nil {
				return nil // graceful shutdown
			}
			w.RecordError(err)
			w.Logger().Warn("Research iteration failed", "error", err)

			// Back off briefly before retrying
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(10 * time.Second):
			}
			continue
		}

		w.IncIterations()
		w.SetStep("Iteration complete, preparing next")

		// Brief pause between iterations to avoid runaway loops
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(2 * time.Second):
		}
	}
}
