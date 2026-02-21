package agent

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// pauseFlagPath returns the cooperative pause flag used by the dashboard.
// Existence of this file means "pause"; removing it means "resume".
func (a *Agent) pauseFlagPath() string {
	if a == nil || a.gh == nil {
		return ""
	}
	repoPath := strings.TrimSpace(a.gh.GetRepoPath())
	if repoPath == "" {
		return ""
	}
	return filepath.Join(repoPath, "Compendium", "_debug", "dashboard_pause.flag")
}

// waitIfPaused blocks while the dashboard pause flag exists.
// This is intentionally checked at safe boundaries to avoid partial writes mid-step.
func (a *Agent) waitIfPaused(ctx context.Context) error {
	p := a.pauseFlagPath()
	if p == "" {
		return nil
	}
	if _, err := os.Stat(p); err != nil {
		return nil
	}

	log.Printf("[Pause] Pause flag detected (%s). Waiting...", p)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if _, err := os.Stat(p); os.IsNotExist(err) {
			log.Printf("[Pause] Resume flag cleared. Continuing.")
			return nil
		}
		time.Sleep(750 * time.Millisecond)
	}
}

