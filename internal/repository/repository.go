package repository

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gitopedia/researcher/internal/github"
)

// RepoManager defines the interface for repository operations (local or remote)
type RepoManager interface {
	github.GitHubClient // Embed the existing GitHubClient interface

	// Local-specific operations
	GetRepoPath() string
	IsLocal() bool
	SetNoCommit(bool)
}

// LocalGitManager implements RepoManager for a local Git repository
type LocalGitManager struct {
	github.GitHubClient
	repoPath string
	noCommit bool
}

func NewLocalGitManager(ctx context.Context, ghClient github.GitHubClient, repoPath string) (*LocalGitManager, error) {
	// Verify repoPath exists and is a git repo
	if _, err := os.Stat(repoPath); err != nil {
		return nil, fmt.Errorf("repo path does not exist: %w", err)
	}
	if _, err := os.Stat(filepath.Join(repoPath, ".git")); err != nil {
		return nil, fmt.Errorf("repo path is not a git repository: %w", err)
	}

	return &LocalGitManager{
		GitHubClient: ghClient,
		repoPath:     repoPath,
	}, nil
}

func (m *LocalGitManager) GetRepoPath() string {
	return m.repoPath
}

func (m *LocalGitManager) IsLocal() bool {
	return true
}

func (m *LocalGitManager) SetNoCommit(val bool) {
	m.noCommit = val
}

// Override CreateBranch to use local git
func (m *LocalGitManager) CreateBranch(baseBranch, newBranch string) error {
	// Check if branch already exists
	if _, err := m.runGit("rev-parse", "--verify", newBranch); err == nil {
		// Branch exists, just checkout
		_, err = m.runGit("checkout", newBranch)
		return err
	}

	// Create and checkout
	_, err := m.runGit("checkout", "-b", newBranch, baseBranch)
	return err
}

// Override CreateFile to use local git
func (m *LocalGitManager) CreateFile(branch, path, message, content string) error {
	fullPath := filepath.Join(m.repoPath, path)
	dir := strings.LastIndex(fullPath, "/")
	if dir != -1 {
		if err := os.MkdirAll(fullPath[:dir], 0755); err != nil {
			return err
		}
	}

	if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
		return err
	}

	if _, err := m.runGit("add", path); err != nil {
		return err
	}

	if m.noCommit {
		return nil
	}

	_, err := m.runGit("commit", "-m", message)
	return err
}

// Override UpdateFile to use local git
func (m *LocalGitManager) UpdateFile(branch, path, message, content, sha string) error {
	// For local git, SHA is not strictly needed for overwriting
	return m.CreateFile(branch, path, message, content)
}

// Override GetFile to use local git
func (m *LocalGitManager) GetFile(ref, path string) (string, string, error) {
	fullPath := filepath.Join(m.repoPath, path)
	content, err := os.ReadFile(fullPath)
	if err != nil {
		return "", "", err
	}
	// We don't really need the SHA for local operations in the same way, but we could generate one if needed.
	return string(content), "", nil
}

// runGit executes a git command in the repo path
func (m *LocalGitManager) runGit(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = m.repoPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("git %s failed: %w (output: %s)", strings.Join(args, " "), err, string(output))
	}
	return string(output), nil
}

