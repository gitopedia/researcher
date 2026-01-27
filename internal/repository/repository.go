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
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
		return err
	}

	// In no-commit mode, just write files without staging or committing
	if m.noCommit {
		return nil
	}

	if _, err := m.runGit("add", path); err != nil {
		return err
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

// ListDirectory lists directories at the given path
func (m *LocalGitManager) ListDirectory(branch, path string) ([]string, error) {
	fullPath := filepath.Join(m.repoPath, path)
	entries, err := os.ReadDir(fullPath)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names, nil
}

// AddBinaryFile adds a binary file to the repository
func (m *LocalGitManager) AddBinaryFile(branch, path, message string, content []byte) error {
	fullPath := filepath.Join(m.repoPath, path)

	// Ensure directory exists
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	if err := os.WriteFile(fullPath, content, 0644); err != nil {
		return err
	}

	// In no-commit mode, just write files without staging or committing
	if m.noCommit {
		return nil
	}

	if _, err := m.runGit("add", path); err != nil {
return err
}

	_, err := m.runGit("commit", "-m", message)
	return err
}

// GetCurrentBranch returns the current git branch name
func (m *LocalGitManager) GetCurrentBranch() (string, error) {
	output, err := m.runGit("rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(output), nil
}

// ResetToMain resets the local repository to the main branch
func (m *LocalGitManager) ResetToMain() error {
	// Checkout main branch
	if _, err := m.runGit("checkout", "main"); err != nil {
		return err
	}
	// Pull latest changes
	_, err := m.runGit("pull", "origin", "main")
	return err
}
// ListFilesInBranch lists all files under a given path in the local repository
func (m *LocalGitManager) ListFilesInBranch(branch, path string) ([]string, error) {
fullPath := filepath.Join(m.repoPath, path)
var files []string

err := filepath.Walk(fullPath, func(filePath string, info os.FileInfo, err error) error {
if err != nil {
return err
}
if !info.IsDir() {
relPath, err := filepath.Rel(m.repoPath, filePath)
if err != nil {
return err
}
files = append(files, filepath.ToSlash(relPath))
}
return nil
})

if err != nil {
return nil, err
}
return files, nil
}

// DeleteFile removes a file from the local repository
func (m *LocalGitManager) DeleteFile(branch, path, message, sha string) error {
fullPath := filepath.Join(m.repoPath, path)

if err := os.Remove(fullPath); err != nil {
return err
}

if m.noCommit {
return nil
}

if _, err := m.runGit("add", path); err != nil {
return err
}

_, err := m.runGit("commit", "-m", message)
return err
}

// UpdatePRBranch merges main into the current branch (for local mode)
func (m *LocalGitManager) UpdatePRBranch(prNumber int) error {
_, err := m.runGit("merge", "main", "--no-edit")
return err
}
