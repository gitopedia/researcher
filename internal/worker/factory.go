package worker

import (
	"log/slog"

	"github.com/gitopedia/researcher/internal/llm"
	"github.com/gitopedia/researcher/internal/queue"
	"github.com/gitopedia/researcher/internal/repository"
	"github.com/gitopedia/researcher/internal/search"
)

// Factory constructs workers of any type, injecting shared dependencies.
type Factory struct {
	QueueMgr *queue.Manager
	Logger   *slog.Logger
	RepoMgr  repository.RepoManager
	Searcher search.Searcher
	LLMGen   llm.Generator
}

// Create returns a Worker for the given Config.
func (f *Factory) Create(cfg Config) Worker {
	switch cfg.Type {
	case TypeResearch:
		return NewResearchWorker(cfg, f.QueueMgr, f.Logger, f.RepoMgr, f.Searcher, f.LLMGen)
	case TypeImagePrompt:
		return NewImagePromptWorker(cfg, f.QueueMgr, f.Logger, f.RepoMgr, f.Searcher, f.LLMGen)
	case TypeImageGen:
		return NewImageGenWorker(cfg, f.QueueMgr, f.Logger, f.RepoMgr, f.Searcher, f.LLMGen)
	default:
		// Return a research worker as fallback
		return NewResearchWorker(cfg, f.QueueMgr, f.Logger, f.RepoMgr, f.Searcher, f.LLMGen)
	}
}
