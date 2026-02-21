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

// ImagePromptWorker continuously scans for articles that need image prompts
// and generates them using the LLM (via the queue).
type ImagePromptWorker struct {
	Base

	repoMgr  repository.RepoManager
	searcher search.Searcher
	llmGen   llm.Generator
}

// NewImagePromptWorker creates an image-prompt worker.
func NewImagePromptWorker(cfg Config, qm *queue.Manager, logger *slog.Logger, repoMgr repository.RepoManager, searcher search.Searcher, llmGen llm.Generator) *ImagePromptWorker {
	return &ImagePromptWorker{
		Base:     NewBase(cfg, qm, logger),
		repoMgr:  repoMgr,
		searcher: searcher,
		llmGen:   llmGen,
	}
}

// Start begins the continuous image prompt generation loop.
func (w *ImagePromptWorker) Start(ctx context.Context) error {
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

	w.Logger().Info("Starting image prompt worker", "branch", branch)

	// Queue-wrapped LLM client
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
			w.Logger().Info("Image prompt worker stopped", "branch", branch)
			return nil
		default:
		}

		w.SetStep("Scanning for articles needing image prompts")

		// Use the agent's existing image prompt generation.
		// GenerateImages handles both prompt generation (Phase 1) and image
		// generation (Phase 2). Since this worker only does prompts, we call
		// the agent method which generates prompts – actual image gen is
		// handled by the image generation worker.
		//
		// NOTE: The agent's generateImages first generates prompts via LLM,
		// then generates images via ComfyUI. For the prompt-only worker we
		// reuse the full call; the ComfyUI phase will simply find nothing to
		// do if prompts already exist (or will be handled gracefully if
		// ComfyUI is unavailable – the queue will wait).
		//
		// A future refinement could split the agent method into two phases
		// that can be called independently.
		if err := ag.GenerateImagesForReview(ctx, branch); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			w.RecordError(err)
			w.Logger().Warn("Image prompt iteration failed", "error", err)

			select {
			case <-ctx.Done():
				return nil
			case <-time.After(15 * time.Second):
			}
			continue
		}

		w.IncIterations()
		w.SetStep("Prompt scan complete, waiting before next scan")

		// Longer pause – prompts don't need to be generated as frequently
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(30 * time.Second):
		}
	}
}
