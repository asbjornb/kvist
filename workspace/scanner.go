package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/asbjornb/kvist/git"
)

// Scanner discovers and scans repositories in workspaces
type Scanner struct {
	config *Config
	cache  *RepoCache
	mu     sync.RWMutex
}

// NewScanner creates a new workspace scanner
func NewScanner(config *Config, cache *RepoCache) *Scanner {
	return &Scanner{
		config: config,
		cache:  cache,
	}
}

// ScanResult represents the result of a repository scan
type ScanResult struct {
	Repos []RepoInfo
	Error error
}

// ScanWorkspaces discovers and scans all repositories in enabled workspaces
func (s *Scanner) ScanWorkspaces(ctx context.Context) <-chan ScanResult {
	results := make(chan ScanResult, 1)

	go func() {
		defer close(results)

		var allRepos []RepoInfo
		var errors []string

		// Discover repos in each workspace
		for _, workspace := range s.config.Workspaces {
			if !workspace.Enabled {
				continue
			}

			repos, err := s.discoverRepos(ctx, workspace)
			if err != nil {
				errors = append(errors, fmt.Sprintf("workspace %s: %v", workspace.Name, err))
				continue
			}

			// Scan each repo for metadata in parallel
			scanned := s.scanRepos(ctx, repos, workspace.Name)
			allRepos = append(allRepos, scanned...)
		}

		// Update cache
		s.mu.Lock()
		s.cache.Repos = make(map[string]RepoInfo)
		for _, repo := range allRepos {
			s.cache.Repos[repo.Path] = repo
		}
		s.mu.Unlock()

		// Save cache to disk
		if err := s.cache.Save(); err != nil {
			errors = append(errors, fmt.Sprintf("failed to save cache: %v", err))
		}

		var resultErr error
		if len(errors) > 0 {
			resultErr = fmt.Errorf("scan errors: %s", strings.Join(errors, "; "))
		}

		select {
		case results <- ScanResult{Repos: allRepos, Error: resultErr}:
		case <-ctx.Done():
		}
	}()

	return results
}

// GetCachedRepos returns cached repository information
func (s *Scanner) GetCachedRepos() []RepoInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	repos := make([]RepoInfo, 0, len(s.cache.Repos))
	for _, repo := range s.cache.Repos {
		repos = append(repos, repo)
	}
	return repos
}

// discoverRepos finds all git repositories in a workspace
func (s *Scanner) discoverRepos(ctx context.Context, workspace Workspace) ([]string, error) {
	var repos []string

	err := filepath.Walk(workspace.Path, func(path string, info os.FileInfo, err error) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err != nil {
			// Skip directories we can't read
			return nil
		}

		// Check if this is a git repository
		if info.IsDir() && info.Name() == ".git" {
			repoPath := filepath.Dir(path)
			repos = append(repos, repoPath)
			return filepath.SkipDir // Don't scan inside .git directories
		}

		// Check for git worktree (bare repo)
		if !info.IsDir() && info.Name() == ".git" {
			repoPath := filepath.Dir(path)
			repos = append(repos, repoPath)
		}

		// Skip hidden directories (except .git which we handle above)
		if info.IsDir() && strings.HasPrefix(info.Name(), ".") && info.Name() != ".git" {
			return filepath.SkipDir
		}

		// Skip common non-repo directories to speed up scan
		if info.IsDir() {
			switch info.Name() {
			case "node_modules", "target", "build", "dist", ".next", ".nuxt", "vendor":
				return filepath.SkipDir
			}
		}

		return nil
	})

	return repos, err
}

// scanRepos scans repository metadata in parallel
func (s *Scanner) scanRepos(ctx context.Context, repoPaths []string, workspaceName string) []RepoInfo {
	type result struct {
		repo RepoInfo
		err  error
	}

	results := make(chan result, len(repoPaths))
	var wg sync.WaitGroup

	// Limit concurrent scans to avoid overwhelming the system
	semaphore := make(chan struct{}, 10)

	for _, repoPath := range repoPaths {
		wg.Add(1)
		go func(path string) {
			defer wg.Done()

			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				return
			}

			repo, err := s.scanRepo(ctx, path, workspaceName)
			results <- result{repo: repo, err: err}
		}(repoPath)
	}

	// Close results channel when all workers finish
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results
	var repos []RepoInfo
	for res := range results {
		if res.err == nil {
			repos = append(repos, res.repo)
		}
		// For now, silently skip repos that failed to scan
		// TODO: Could add logging or error collection here
	}

	return repos
}

// scanRepo scans a single repository for metadata
func (s *Scanner) scanRepo(ctx context.Context, repoPath, workspaceName string) (RepoInfo, error) {
	select {
	case <-ctx.Done():
		return RepoInfo{}, ctx.Err()
	default:
	}

	repo := RepoInfo{
		Path:          repoPath,
		Name:          filepath.Base(repoPath),
		WorkspaceName: workspaceName,
		LastScanned:   time.Now(),
	}

	// Check if we have cached info that's recent enough (< 5 minutes old)
	s.mu.RLock()
	if cached, exists := s.cache.Repos[repoPath]; exists {
		if time.Since(cached.LastScanned) < 5*time.Minute {
			s.mu.RUnlock()
			return cached, nil
		}
	}
	s.mu.RUnlock()

	// Get current branch
	if branch, err := git.GetCurrentBranch(repoPath); err == nil {
		repo.Branch = branch
	}

	// Get ahead/behind info
	if ahead, behind, ok := git.GetAheadBehind(repoPath); ok {
		repo.Ahead = ahead
		repo.Behind = behind
		repo.HasUpstream = true
	}

	// Get last commit time
	if commits, err := git.GetCommits(repoPath, 1); err == nil && len(commits) > 0 {
		repo.LastCommitTime = commits[0].Time
	}

	return repo, nil
}

// GetRepo returns repository information by path
func (s *Scanner) GetRepo(path string) (RepoInfo, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	repo, exists := s.cache.Repos[path]
	return repo, exists
}

// UpdateRepo updates a single repository's metadata
func (s *Scanner) UpdateRepo(ctx context.Context, repoPath string) error {
	// Find which workspace this repo belongs to
	var workspaceName string
	for _, ws := range s.config.Workspaces {
		if strings.HasPrefix(repoPath, ws.Path) {
			workspaceName = ws.Name
			break
		}
	}

	repo, err := s.scanRepo(ctx, repoPath, workspaceName)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.cache.Repos[repoPath] = repo
	s.mu.Unlock()

	return s.cache.Save()
}