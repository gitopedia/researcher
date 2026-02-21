// Package worker provides a multi-branch, multi-type worker system for the
// researcher application. Workers are spawned against a branch (issue) and
// execute a continuous loop until stopped, routing LLM/ComfyUI calls through
// the queue system.
package worker

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/gitopedia/researcher/internal/queue"
)

// Type identifies the kind of work a worker performs.
type Type string

const (
	TypeResearch    Type = "research"
	TypeImagePrompt Type = "image_prompt"
	TypeImageGen    Type = "image_gen"
)

// AllTypes returns the canonical list of worker types.
func AllTypes() []Type {
	return []Type{TypeResearch, TypeImagePrompt, TypeImageGen}
}

// State represents the lifecycle state of a worker.
type State string

const (
	StateStopped  State = "stopped"
	StateStarting State = "starting"
	StateRunning  State = "running"
	StatePaused   State = "paused"
	StateStopping State = "stopping"
	StateError    State = "error"
)

// Config holds the configuration for a single worker instance.
type Config struct {
	ID       string `json:"id"`
	Type     Type   `json:"type"`
	Branch   string `json:"branch"`
	RepoPath string `json:"repoPath"` // path to the gitopedia repo
	Enabled  bool   `json:"enabled"`
}

// Status is a snapshot of a worker's current state (JSON-safe).
type Status struct {
	ID          string    `json:"id"`
	Type        Type      `json:"type"`
	State       State     `json:"state"`
	Branch      string    `json:"branch"`
	CurrentStep string    `json:"currentStep,omitempty"`
	Iterations  int64     `json:"iterations"`
	Errors      int64     `json:"errors"`
	LastError   string    `json:"lastError,omitempty"`
	StartedAt   time.Time `json:"startedAt,omitempty"`
	Enabled     bool      `json:"enabled"`
}

// Worker is the interface every worker type must implement.
type Worker interface {
	// ID returns the unique identifier for this worker.
	ID() string

	// Type returns the worker type.
	Type() Type

	// Start begins the worker's loop. Blocks until the context is cancelled
	// or Stop is called.
	Start(ctx context.Context) error

	// Stop signals the worker to stop gracefully.
	Stop()

	// Pause pauses the worker's loop.
	Pause()

	// Resume resumes a paused worker.
	Resume()

	// Status returns the current status.
	Status() Status

	// Configure updates the worker configuration.
	Configure(cfg Config) error
}

// Base provides common fields and helpers shared by all worker implementations.
type Base struct {
	mu          sync.RWMutex
	id          string
	workerType  Type
	state       State
	branch      string
	repoPath    string
	currentStep string
	iterations  int64
	errors      int64
	lastError   string
	startedAt   time.Time
	enabled     bool

	queue  *queue.Manager
	logger *slog.Logger

	cancel  context.CancelFunc
	pauseCh chan struct{}
	done    chan struct{}
}

// NewBase creates a new Base with the given configuration.
func NewBase(cfg Config, qm *queue.Manager, logger *slog.Logger) Base {
	return Base{
		id:         cfg.ID,
		workerType: cfg.Type,
		state:      StateStopped,
		branch:     cfg.Branch,
		repoPath:   cfg.RepoPath,
		enabled:    cfg.Enabled,
		queue:      qm,
		logger:     logger.With("worker", cfg.ID, "type", string(cfg.Type)),
		pauseCh:    make(chan struct{}, 1),
		done:       make(chan struct{}),
	}
}

// --- Getters ---

func (b *Base) ID() string   { return b.id }
func (b *Base) Type() Type   { return b.workerType }
func (b *Base) Branch() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.branch
}

func (b *Base) Status() Status {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return Status{
		ID:          b.id,
		Type:        b.workerType,
		State:       b.state,
		Branch:      b.branch,
		CurrentStep: b.currentStep,
		Iterations:  b.iterations,
		Errors:      b.errors,
		LastError:   b.lastError,
		StartedAt:   b.startedAt,
		Enabled:     b.enabled,
	}
}

// --- State helpers ---

func (b *Base) SetState(s State)       { b.mu.Lock(); b.state = s; b.mu.Unlock() }
func (b *Base) SetStep(step string)    { b.mu.Lock(); b.currentStep = step; b.mu.Unlock() }
func (b *Base) IncIterations()         { b.mu.Lock(); b.iterations++; b.mu.Unlock() }
func (b *Base) RecordError(err error)  {
	b.mu.Lock()
	b.errors++
	if err != nil {
		b.lastError = err.Error()
	}
	b.mu.Unlock()
}

func (b *Base) Configure(cfg Config) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.branch = cfg.Branch
	b.repoPath = cfg.RepoPath
	b.enabled = cfg.Enabled
	return nil
}

// Stop signals the worker to stop via its cancel function.
func (b *Base) Stop() {
	if b.cancel != nil {
		b.cancel()
	}
}

// Pause signals the worker to pause.
func (b *Base) Pause() {
	b.SetState(StatePaused)
}

// Resume signals a paused worker to continue.
func (b *Base) Resume() {
	b.SetState(StateRunning)
	select {
	case b.pauseCh <- struct{}{}:
	default:
	}
}

// WaitIfPaused blocks if the worker is paused, returning an error if the
// context is cancelled while waiting.
func (b *Base) WaitIfPaused(ctx context.Context) error {
	for {
		b.mu.RLock()
		s := b.state
		b.mu.RUnlock()
		if s != StatePaused {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-b.pauseCh:
			return nil
		}
	}
}

// Logger returns the worker's logger.
func (b *Base) Logger() *slog.Logger { return b.logger }

// Queue returns the queue manager.
func (b *Base) Queue() *queue.Manager { return b.queue }
