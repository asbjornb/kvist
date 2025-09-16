package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestConfig(t *testing.T) {
	// Test creating a config with some workspaces
	config := &Config{
		Version: 1,
		Workspaces: []Workspace{
			{Name: "test1", Path: "/tmp/test1"},
			{Name: "test2", Path: "/tmp/test2"},
		},
	}

	// Test adding a workspace
	config.Workspaces = append(config.Workspaces, Workspace{
		Name: "test3", Path: "/tmp/test3",
	})

	if len(config.Workspaces) != 3 {
		t.Errorf("Expected 3 workspaces, got %d", len(config.Workspaces))
	}

	// Test finding workspaces by name
	var found *Workspace
	for _, ws := range config.Workspaces {
		if ws.Name == "test2" {
			found = &ws
			break
		}
	}

	if found == nil {
		t.Errorf("Could not find workspace 'test2'")
	}
}

func TestRepoCache(t *testing.T) {
	cache := &RepoCache{
		Version: time.Now(),
		Repos:   make(map[string]RepoInfo),
	}

	// Add a repo to cache
	repo := RepoInfo{
		Path:           "/tmp/test-repo",
		Name:           "test-repo",
		Branch:         "main",
		Ahead:          2,
		Behind:         0,
		HasUpstream:    true,
		LastCommitTime: time.Now().Add(-24 * time.Hour),
		LastScanned:    time.Now(),
		WorkspaceName:  "test",
	}

	cache.Repos[repo.Path] = repo

	// Test retrieving repo
	retrieved, exists := cache.Repos["/tmp/test-repo"]
	if !exists {
		t.Errorf("Repo not found in cache")
	}

	if retrieved.Name != "test-repo" {
		t.Errorf("Expected repo name 'test-repo', got '%s'", retrieved.Name)
	}

	if retrieved.Ahead != 2 {
		t.Errorf("Expected ahead count 2, got %d", retrieved.Ahead)
	}
}

func TestScanner(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "kvist_test_workspace")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a fake git repository structure
	testRepo := filepath.Join(tempDir, "test-repo")
	if err := os.MkdirAll(testRepo, 0755); err != nil {
		t.Fatalf("Failed to create test repo dir: %v", err)
	}

	gitDir := filepath.Join(testRepo, ".git")
	if err := os.MkdirAll(gitDir, 0755); err != nil {
		t.Fatalf("Failed to create .git dir: %v", err)
	}

	// Create config and cache
	config := &Config{
		Version: 1,
		Workspaces: []Workspace{
			{Name: "test", Path: tempDir},
		},
	}

	cache := &RepoCache{
		Version: time.Now(),
		Repos:   make(map[string]RepoInfo),
	}

	scanner := NewScanner(config, cache)

	// Test repo discovery
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	repos, err := scanner.discoverRepos(ctx, config.Workspaces[0])
	if err != nil {
		t.Fatalf("Failed to discover repos: %v", err)
	}

	if len(repos) != 1 {
		t.Errorf("Expected 1 repo, found %d", len(repos))
	}

	if len(repos) > 0 && repos[0] != testRepo {
		t.Errorf("Expected repo path %s, got %s", testRepo, repos[0])
	}

	t.Logf("Successfully discovered repo: %s", repos[0])
}