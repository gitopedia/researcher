package agent

import (
	"log"
	"sync"
)

// ProgressTracker prints progress as regular log lines after each step
type ProgressTracker struct {
	mu      sync.Mutex
	current int
	total   int
	phase   Phase
	topic   string
}

// Phase represents the current phase of research
type Phase int

const (
	PhaseInitialGathering Phase = iota
	PhaseDetailedGathering
)

func (p Phase) String() string {
	switch p {
	case PhaseInitialGathering:
		return "Initial Information Gathering"
	case PhaseDetailedGathering:
		return "Detailed Information Gathering"
	default:
		return "Unknown"
	}
}

// NewProgressTracker creates a new progress tracker
func NewProgressTracker() *ProgressTracker {
	return &ProgressTracker{
		phase: PhaseInitialGathering,
	}
}

// SetPhase updates the current phase
func (pt *ProgressTracker) SetPhase(phase Phase) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	pt.phase = phase
}

// SetTopic updates the current topic being processed
func (pt *ProgressTracker) SetTopic(topic string) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	pt.topic = topic
	pt.current = 0 // Reset count for new topic
}

// SetTotal sets the target number of sources
func (pt *ProgressTracker) SetTotal(total int) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	pt.total = total
}

// Update prints progress as a log line after an LLM step completes
func (pt *ProgressTracker) Update(current int) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	pt.current = current
	pt.printProgress()
}

// Increment increments the progress by 1 and prints it
func (pt *ProgressTracker) Increment() {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	pt.current++
	pt.printProgress()
}

// printProgress prints the current progress as a log line
func (pt *ProgressTracker) printProgress() {
	if pt.total > 0 {
		percent := float64(pt.current) / float64(pt.total) * 100
		if pt.topic != "" {
			log.Printf("[%s] %s - Progress: %d/%d sources (%.1f%%)", 
				pt.phase.String(), pt.topic, pt.current, pt.total, percent)
		} else {
			log.Printf("[%s] Progress: %d/%d sources (%.1f%%)", 
				pt.phase.String(), pt.current, pt.total, percent)
		}
	} else {
		if pt.topic != "" {
			log.Printf("[%s] %s - Progress: %d sources", 
				pt.phase.String(), pt.topic, pt.current)
		} else {
			log.Printf("[%s] Progress: %d sources", 
				pt.phase.String(), pt.current)
		}
	}
}

// Finish is a no-op now (no cleanup needed)
func (pt *ProgressTracker) Finish() {
	// Nothing to clean up - progress is just log lines
}

