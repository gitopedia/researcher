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

// ImageGenWorker continuously scans for pending image prompts and generates
// images using ComfyUI (via the queue).
type ImageGenWorker struct {
	Base

	repoMgr  repository.RepoManager
	searcher search.Searcher
	llmGen   llm.Generator
}

// NewImageGenWorker creates an image generation worker.
func NewImageGenWorker(cfg Config, qm *queue.Manager, logger *slog.Logger, repoMgr repository.RepoManager, searcher search.Searcher, llmGen llm.Generator) *ImageGenWorker {
	return &ImageGenWorker{
		Base:     NewBase(cfg, qm, logger),
		repoMgr:  repoMgr,
		searcher: searcher,
		llmGen:   llmGen,
	}
}

// Start begins the continuous image generation loop.
func (w *ImageGenWorker) Start(ctx context.Context) error {
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

	w.Logger().Info("Starting image generation worker", "branch", branch)

	// Even image generation may need LLM calls (for prompt refinement),
	// so wrap through the queue.
	queuedLLM := queue.NewQueuedLLMClient(w.llmGen, w.Queue().LLM)
	ag := agent.NewAgentWithDeps(w.repoMgr, w.searcher, queuedLLM)
	ag.SetNoCommit(false)

	w.SetState(StateRunning)

	for {
		if err := w.WaitIfPaused(ctx); err != nil {
			return err
		}

		select {
		case <-ctx.Done():
			w.Logger().Info("Image generation worker stopped", "branch", branch)
			return nil
		default:
		}

		w.SetStep("Generating images from pending prompts")

		// The agent's GenerateImagesForReview generates prompts (Phase 1)
		// then images (Phase 2). The prompt phase will be a no-op if prompts
		// already exist; the image phase uses ComfyUI.
		if err := ag.GenerateImagesForReview(ctx, branch); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			w.RecordError(err)
			w.Logger().Warn("Image generation iteration failed", "error", err)

			select {
			case <-ctx.Done():
				return nil
			case <-time.After(30 * time.Second):
			}
			continue
		}

		w.IncIterations()
		w.SetStep("Image generation scan complete, waiting")

		// Image generation is resource-intensive; pause longer.
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(60 * time.Second):
		}
	}
}
