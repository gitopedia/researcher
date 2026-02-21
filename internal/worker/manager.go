package worker

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/gitopedia/researcher/internal/queue"
)

// Manager tracks all workers and prevents duplicate workers per branch+type.
type Manager struct {
	mu      sync.RWMutex
	workers map[string]Worker // keyed by worker ID
	queue   *queue.Manager
	logger  *slog.Logger
}

// NewManager creates a new worker manager.
func NewManager(qm *queue.Manager, logger *slog.Logger) *Manager {
	return &Manager{
		workers: make(map[string]Worker),
		queue:   qm,
		logger:  logger.With("component", "worker-manager"),
	}
}

// Register adds a worker to the manager. Returns an error if a worker with
// the same ID already exists, or if a worker of the same type is already
// running on the same branch.
func (m *Manager) Register(w Worker) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.workers[w.ID()]; exists {
		return fmt.Errorf("worker %q already exists", w.ID())
	}

	m.workers[w.ID()] = w
	m.logger.Info("Worker registered", "id", w.ID(), "type", w.Type())
	return nil
}

// Remove removes a worker from the manager. The worker must be stopped first.
func (m *Manager) Remove(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	w, exists := m.workers[id]
	if !exists {
		return fmt.Errorf("worker %q not found", id)
	}

	s := w.Status()
	if s.State != StateStopped && s.State != StateError {
		return fmt.Errorf("worker %q must be stopped before removal (state: %s)", id, s.State)
	}

	delete(m.workers, id)
	m.logger.Info("Worker removed", "id", id)
	return nil
}

// StartWorker starts a specific worker by ID.
func (m *Manager) StartWorker(ctx context.Context, id string) error {
	m.mu.RLock()
	w, exists := m.workers[id]
	m.mu.RUnlock()

	if !exists {
		return fmt.Errorf("worker %q not found", id)
	}

	st := w.Status()

	// Prevent running duplicate worker types on the same branch
	if err := m.checkBranchConflict(w.ID(), w.Type(), st.Branch); err != nil {
		return err
	}

	if st.State != StateStopped && st.State != StateError {
		return fmt.Errorf("worker %q is already in state %s", id, st.State)
	}

	go func() {
		if err := w.Start(ctx); err != nil {
			m.logger.Error("Worker exited with error", "id", id, "error", err)
		}
	}()
	return nil
}

// StopWorker stops a specific worker by ID.
func (m *Manager) StopWorker(id string) error {
	m.mu.RLock()
	w, exists := m.workers[id]
	m.mu.RUnlock()

	if !exists {
		return fmt.Errorf("worker %q not found", id)
	}

	w.Stop()
	return nil
}

// PauseWorker pauses a specific worker by ID.
func (m *Manager) PauseWorker(id string) error {
	m.mu.RLock()
	w, exists := m.workers[id]
	m.mu.RUnlock()

	if !exists {
		return fmt.Errorf("worker %q not found", id)
	}

	w.Pause()
	return nil
}

// ResumeWorker resumes a specific worker by ID.
func (m *Manager) ResumeWorker(id string) error {
	m.mu.RLock()
	w, exists := m.workers[id]
	m.mu.RUnlock()

	if !exists {
		return fmt.Errorf("worker %q not found", id)
	}

	w.Resume()
	return nil
}

// ConfigureWorker updates a worker's configuration.
func (m *Manager) ConfigureWorker(id string, cfg Config) error {
	m.mu.RLock()
	w, exists := m.workers[id]
	m.mu.RUnlock()

	if !exists {
		return fmt.Errorf("worker %q not found", id)
	}

	return w.Configure(cfg)
}

// GetStatus returns the status of all workers.
func (m *Manager) GetStatus() []Status {
	m.mu.RLock()
	defer m.mu.RUnlock()

	statuses := make([]Status, 0, len(m.workers))
	for _, w := range m.workers {
		statuses = append(statuses, w.Status())
	}
	return statuses
}

// GetWorkerStatus returns the status of a single worker.
func (m *Manager) GetWorkerStatus(id string) (Status, error) {
	m.mu.RLock()
	w, exists := m.workers[id]
	m.mu.RUnlock()

	if !exists {
		return Status{}, fmt.Errorf("worker %q not found", id)
	}
	return w.Status(), nil
}

// GetWorker returns the worker with the given ID.
func (m *Manager) GetWorker(id string) (Worker, bool) {
	m.mu.RLock()
	w, exists := m.workers[id]
	m.mu.RUnlock()
	return w, exists
}

// StopAll stops all running workers.
func (m *Manager) StopAll() {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, w := range m.workers {
		s := w.Status()
		if s.State == StateRunning || s.State == StatePaused || s.State == StateStarting {
			w.Stop()
		}
	}
}

// checkBranchConflict returns an error if another worker of the same type
// is already running on the given branch.
func (m *Manager) checkBranchConflict(excludeID string, wType Type, branch string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if branch == "" {
		return nil // no branch set yet, can't conflict
	}

	for _, w := range m.workers {
		if w.ID() == excludeID {
			continue
		}
		s := w.Status()
		if w.Type() == wType && s.Branch == branch &&
			(s.State == StateRunning || s.State == StatePaused || s.State == StateStarting) {
			return fmt.Errorf("worker %q (%s) is already running on branch %q", w.ID(), wType, branch)
		}
	}
	return nil
}

// CreateDefaultWorkers creates one stopped worker of each type and registers
// them. This is called on startup to populate the UI with default workers.
func (m *Manager) CreateDefaultWorkers(repoPath string, factory func(cfg Config) Worker) {
	defaults := []Config{
		{ID: "research-default", Type: TypeResearch, RepoPath: repoPath, Enabled: true},
		{ID: "imageprompt-default", Type: TypeImagePrompt, RepoPath: repoPath, Enabled: true},
		{ID: "imagegen-default", Type: TypeImageGen, RepoPath: repoPath, Enabled: true},
	}
	for _, cfg := range defaults {
		w := factory(cfg)
		if err := m.Register(w); err != nil {
			m.logger.Warn("Failed to register default worker", "id", cfg.ID, "error", err)
		}
	}
}
