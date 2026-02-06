package api

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// GitManager handles git operations for the repository
type GitManager struct {
	repoPath string
}

// CommitOptions controls how commits are created.
type CommitOptions struct {
	// Paths is a list of repo-relative paths to stage. If empty, stages all changes.
	Paths []string
	// Message is the commit message to use.
	Message string
	// SkipOnMain skips committing when on main/master.
	SkipOnMain bool
}

// BranchInfo contains information about the current git branch
type BranchInfo struct {
	Name     string `json:"name"`
	IsMain   bool   `json:"isMain"`
	IsDirty  bool   `json:"isDirty"`
	Ahead    int    `json:"ahead"`
	Behind   int    `json:"behind"`
}

// NewGitManager creates a new git manager
func NewGitManager(repoPath string) *GitManager {
	return &GitManager{
		repoPath: repoPath,
	}
}

// GetBranchInfo returns information about the current branch
func (g *GitManager) GetBranchInfo() (*BranchInfo, error) {
	info := &BranchInfo{}

	// Get current branch name
	output, err := g.RunGit("rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("failed to get branch name: %w", err)
	}
	info.Name = strings.TrimSpace(output)
	info.IsMain = info.Name == "main" || info.Name == "master"

	// Check if working directory is dirty
	output, err = g.RunGit("status", "--porcelain")
	if err == nil {
		info.IsDirty = strings.TrimSpace(output) != ""
	}

	// Get ahead/behind counts (may fail if no upstream)
	output, err = g.RunGit("rev-list", "--left-right", "--count", "HEAD...@{upstream}")
	if err == nil {
		parts := strings.Fields(output)
		if len(parts) >= 2 {
			fmt.Sscanf(parts[0], "%d", &info.Ahead)
			fmt.Sscanf(parts[1], "%d", &info.Behind)
		}
	}

	return info, nil
}

// Clean removes generated content from the repository
func (g *GitManager) Clean(cleanImages, cleanArticles bool) error {
	incomingPath := filepath.Join(g.repoPath, "Compendium", "_incoming")

	if cleanImages {
		// Clean images from _incoming root
		entries, err := os.ReadDir(incomingPath)
		if err == nil {
			for _, entry := range entries {
				if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".png") {
					os.Remove(filepath.Join(incomingPath, entry.Name()))
				}
			}
		}

		// Clean indexes subdirectories
		indexDirs := []string{"indexes/domains", "indexes/categories", "indexes/topics"}
		for _, dir := range indexDirs {
			dirPath := filepath.Join(incomingPath, dir)
			entries, err := os.ReadDir(dirPath)
			if err != nil {
				continue
			}
			for _, entry := range entries {
				if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".png") {
					os.Remove(filepath.Join(dirPath, entry.Name()))
				}
			}
		}
	}

	if cleanArticles {
		// Clean markdown files from _incoming root
		entries, err := os.ReadDir(incomingPath)
		if err == nil {
			for _, entry := range entries {
				if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".md") {
					os.Remove(filepath.Join(incomingPath, entry.Name()))
				}
			}
		}

		// Clean sources directory
		sourcesPath := filepath.Join(incomingPath, "sources")
		os.RemoveAll(sourcesPath)

		// Clean _debug directory
		debugPath := filepath.Join(g.repoPath, "Compendium", "_debug")
		os.RemoveAll(debugPath)
	}

	return nil
}

// CheckoutMain switches to the main branch
func (g *GitManager) CheckoutMain() error {
	_, err := g.RunGit("checkout", "main")
	return err
}

// ResetHard resets the current branch to origin
func (g *GitManager) ResetHard() error {
	// Fetch first
	g.RunGit("fetch", "origin")
	
	// Get current branch
	output, err := g.RunGit("rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return err
	}
	branch := strings.TrimSpace(output)
	
	// Reset to origin
	_, err = g.RunGit("reset", "--hard", "origin/"+branch)
	return err
}

// RunGit executes a git command in the repository
func (g *GitManager) RunGit(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = g.repoPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("git %s failed: %w (output: %s)", 
			strings.Join(args, " "), err, string(output))
	}
	return string(output), nil
}

// StageAndCommit stages changes and creates a local commit if there are staged changes.
// Returns (committed, commitHash, stagedFiles, error).
func (g *GitManager) StageAndCommit(opts CommitOptions) (bool, string, []string, error) {
	if opts.Message == "" {
		return false, "", nil, fmt.Errorf("commit message is required")
	}

	branch, err := g.GetBranchInfo()
	if err == nil && opts.SkipOnMain && branch.IsMain {
		return false, "", nil, nil
	}

	// Stage changes
	if len(opts.Paths) == 0 {
		if _, err := g.RunGit("add", "-A"); err != nil {
			return false, "", nil, err
		}
	} else {
		args := []string{"add", "-A", "--"}
		args = append(args, opts.Paths...)
		if _, err := g.RunGit(args...); err != nil {
			return false, "", nil, err
		}
	}

	// Check if anything is staged
	stagedOut, err := g.RunGit("diff", "--cached", "--name-only")
	if err != nil {
		return false, "", nil, err
	}
	var stagedFiles []string
	for _, line := range strings.Split(stagedOut, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		stagedFiles = append(stagedFiles, line)
	}
	if len(stagedFiles) == 0 {
		return false, "", nil, nil
	}

	// Commit
	if _, err := g.RunGit("commit", "-m", opts.Message); err != nil {
		return false, "", stagedFiles, err
	}

	// Grab commit hash
	hashOut, err := g.RunGit("rev-parse", "HEAD")
	if err != nil {
		// Commit succeeded; treat hash lookup failure as non-fatal.
		return true, "", stagedFiles, nil
	}
	return true, strings.TrimSpace(hashOut), stagedFiles, nil
}
