// Package queue provides a job queue with retry logic and health checking
// for LLM and ComfyUI service calls. Workers submit jobs to the queue and
// block until results are available. The queue processes jobs sequentially,
// retrying with backoff when the underlying service is unavailable.
package queue

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// JobType identifies the service a job targets.
type JobType string

const (
	JobTypeLLM     JobType = "llm"
	JobTypeComfyUI JobType = "comfyui"
)

// Job represents a unit of work submitted to the queue.
type Job struct {
	ID        string
	Type      JobType
	Label     string // human-readable description for logging
	Execute   func(ctx context.Context) (interface{}, error)
	resultCh  chan Result
	CreatedAt time.Time
}

// Result carries the outcome of a processed job.
type Result struct {
	Data  interface{}
	Error error
}

// Stats exposes the current queue statistics (read-only snapshot).
type Stats struct {
	Name         string    `json:"name"`
	Pending      int       `json:"pending"`
	Processing   bool      `json:"processing"`
	CurrentJob   string    `json:"currentJob,omitempty"`
	ServiceUp    bool      `json:"serviceUp"`
	TotalJobs    int64     `json:"totalJobs"`
	TotalErrors  int64     `json:"totalErrors"`
	LastError    string    `json:"lastError,omitempty"`
	LastSuccess  time.Time `json:"lastSuccess,omitempty"`
}

// Queue processes jobs sequentially with automatic retry and health-gating.
type Queue struct {
	name string
	jobs chan *Job

	// healthCheck returns true when the target service is reachable.
	healthCheck func(ctx context.Context) bool

	retryDelay    time.Duration
	maxRetryDelay time.Duration
	maxRetries    int

	logger *slog.Logger

	// stats
	mu          sync.RWMutex
	processing  bool
	currentJob  string
	serviceUp   bool
	lastError   string
	lastSuccess time.Time
	totalJobs   atomic.Int64
	totalErrors atomic.Int64

	cancel context.CancelFunc
	done   chan struct{}
}

// Option configures a Queue.
type Option func(*Queue)

// WithRetryDelay sets the initial delay between retries (default 5s).
func WithRetryDelay(d time.Duration) Option {
	return func(q *Queue) { q.retryDelay = d }
}

// WithMaxRetryDelay caps the exponential backoff (default 2m).
func WithMaxRetryDelay(d time.Duration) Option {
	return func(q *Queue) { q.maxRetryDelay = d }
}

// WithMaxRetries sets how many times a single job is retried (default 0 = unlimited).
func WithMaxRetries(n int) Option {
	return func(q *Queue) { q.maxRetries = n }
}

// WithLogger provides a named logger (default is slog.Default()).
func WithLogger(l *slog.Logger) Option {
	return func(q *Queue) { q.logger = l }
}

// WithBufferSize sets the channel buffer depth (default 256).
func WithBufferSize(n int) Option {
	return func(q *Queue) {
		// Recreate with new capacity – only valid before Start.
		q.jobs = make(chan *Job, n)
	}
}

// New creates a new Queue. healthCheck is called to gate processing;
// when it returns false the queue pauses and retries periodically.
func New(name string, healthCheck func(ctx context.Context) bool, opts ...Option) *Queue {
	q := &Queue{
		name:          name,
		jobs:          make(chan *Job, 256),
		healthCheck:   healthCheck,
		retryDelay:    5 * time.Second,
		maxRetryDelay: 2 * time.Minute,
		maxRetries:    0, // unlimited
		logger:        slog.Default().With("component", "queue", "queue", name),
		done:          make(chan struct{}),
	}
	for _, o := range opts {
		o(q)
	}
	return q
}

// Start begins processing jobs in a background goroutine.
func (q *Queue) Start(ctx context.Context) {
	ctx, q.cancel = context.WithCancel(ctx)
	go q.processLoop(ctx)
}

// Stop signals the queue to drain and exit.
func (q *Queue) Stop() {
	if q.cancel != nil {
		q.cancel()
	}
	<-q.done
}

// Submit enqueues a job and blocks until the result is available or the
// context is cancelled.
func (q *Queue) Submit(ctx context.Context, jobType JobType, label string, execute func(ctx context.Context) (interface{}, error)) (interface{}, error) {
	job := &Job{
		ID:        fmt.Sprintf("%s-%d", q.name, q.totalJobs.Add(1)),
		Type:      jobType,
		Label:     label,
		Execute:   execute,
		resultCh:  make(chan Result, 1),
		CreatedAt: time.Now(),
	}

	select {
	case q.jobs <- job:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	select {
	case res := <-job.resultCh:
		return res.Data, res.Error
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// GetStats returns a snapshot of the queue's current state.
func (q *Queue) GetStats() Stats {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return Stats{
		Name:        q.name,
		Pending:     len(q.jobs),
		Processing:  q.processing,
		CurrentJob:  q.currentJob,
		ServiceUp:   q.serviceUp,
		TotalJobs:   q.totalJobs.Load(),
		TotalErrors: q.totalErrors.Load(),
		LastError:   q.lastError,
		LastSuccess: q.lastSuccess,
	}
}

func (q *Queue) setProcessing(active bool, label string) {
	q.mu.Lock()
	q.processing = active
	q.currentJob = label
	q.mu.Unlock()
}

func (q *Queue) setServiceUp(up bool) {
	q.mu.Lock()
	q.serviceUp = up
	q.mu.Unlock()
}

func (q *Queue) recordError(err error) {
	q.totalErrors.Add(1)
	q.mu.Lock()
	q.lastError = err.Error()
	q.mu.Unlock()
}

func (q *Queue) recordSuccess() {
	q.mu.Lock()
	q.lastError = ""
	q.lastSuccess = time.Now()
	q.mu.Unlock()
}

// processLoop is the main loop that pulls jobs off the channel and executes them.
func (q *Queue) processLoop(ctx context.Context) {
	defer close(q.done)

	for {
		select {
		case <-ctx.Done():
			// Drain remaining jobs and cancel them
			q.drainJobs(ctx)
			return
		case job := <-q.jobs:
			q.processJob(ctx, job)
		}
	}
}

func (q *Queue) processJob(ctx context.Context, job *Job) {
	q.setProcessing(true, job.Label)
	defer q.setProcessing(false, "")

	q.logger.Info("Processing job", "id", job.ID, "label", job.Label)

	delay := q.retryDelay
	attempt := 0

	for {
		// Health-gate: wait until the service is available.
		if !q.waitForService(ctx) {
			job.resultCh <- Result{Error: ctx.Err()}
			return
		}

		attempt++
		result, err := job.Execute(ctx)
		if err == nil {
			q.recordSuccess()
			q.logger.Info("Job completed", "id", job.ID, "attempt", attempt)
			job.resultCh <- Result{Data: result}
			return
		}

		q.recordError(err)
		q.logger.Warn("Job failed", "id", job.ID, "attempt", attempt, "error", err)

		if q.maxRetries > 0 && attempt >= q.maxRetries {
			q.logger.Error("Job exhausted retries", "id", job.ID, "attempts", attempt)
			job.resultCh <- Result{Error: fmt.Errorf("exhausted %d retries: %w", attempt, err)}
			return
		}

		// Wait before retrying
		select {
		case <-ctx.Done():
			job.resultCh <- Result{Error: ctx.Err()}
			return
		case <-time.After(delay):
		}

		// Exponential backoff
		delay = delay * 2
		if delay > q.maxRetryDelay {
			delay = q.maxRetryDelay
		}
	}
}

// waitForService blocks until the health check passes or the context is done.
func (q *Queue) waitForService(ctx context.Context) bool {
	if q.healthCheck(ctx) {
		q.setServiceUp(true)
		return true
	}

	q.setServiceUp(false)
	q.logger.Warn("Service unavailable, waiting...")

	ticker := time.NewTicker(q.retryDelay)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
			if q.healthCheck(ctx) {
				q.setServiceUp(true)
				q.logger.Info("Service available again")
				return true
			}
			q.logger.Debug("Still waiting for service...")
		}
	}
}

// drainJobs cancels all pending jobs when the queue is shutting down.
func (q *Queue) drainJobs(ctx context.Context) {
	for {
		select {
		case job := <-q.jobs:
			job.resultCh <- Result{Error: fmt.Errorf("queue shutting down")}
		default:
			return
		}
	}
}
