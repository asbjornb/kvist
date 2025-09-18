package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
		successfulWorkspaces := make(map[string]bool)

		// Discover repos in each workspace
		for _, workspace := range s.config.Workspaces {

			repos, err := s.discoverRepos(ctx, workspace)
			if err != nil {
				errors = append(errors, fmt.Sprintf("workspace %s: %v", workspace.Name, err))
				continue
			}

			// Scan each repo for metadata in parallel
			scanned := s.scanRepos(ctx, repos, workspace.Name)
			allRepos = append(allRepos, scanned...)
			successfulWorkspaces[workspace.Name] = true
		}

		// Update cache - only clear repos from successfully scanned workspaces
		s.mu.Lock()

		// Remove old repos only from successfully scanned workspaces
		for path, repo := range s.cache.Repos {
			if successfulWorkspaces[repo.WorkspaceName] {
				delete(s.cache.Repos, path)
			}
		}

		// Add all newly discovered repos
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

// GetCachedRepos returns cached repository information sorted by last commit time
func (s *Scanner) GetCachedRepos() []RepoInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	repos := make([]RepoInfo, 0, len(s.cache.Repos))
	for _, repo := range s.cache.Repos {
		repos = append(repos, repo)
	}

	// Sort by last commit time (most recent first)
	// Repos without commit time go to the end
	sort.Slice(repos, func(i, j int) bool {
		// If both have commit times, sort by most recent first
		if !repos[i].LastCommitTime.IsZero() && !repos[j].LastCommitTime.IsZero() {
			return repos[i].LastCommitTime.After(repos[j].LastCommitTime)
		}
		// If only one has commit time, it goes first
		if !repos[i].LastCommitTime.IsZero() && repos[j].LastCommitTime.IsZero() {
			return true
		}
		if repos[i].LastCommitTime.IsZero() && !repos[j].LastCommitTime.IsZero() {
			return false
		}
		// Both have no commit time, sort by name
		return repos[i].Name < repos[j].Name
	})

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

// ScanSingleWorkspace scans a single workspace and updates the cache
func (s *Scanner) ScanSingleWorkspace(ctx context.Context, workspace Workspace) <-chan ScanResult {
	results := make(chan ScanResult, 1)

	go func() {
		defer close(results)

		repos, err := s.discoverRepos(ctx, workspace)
		if err != nil {
			select {
			case results <- ScanResult{Repos: nil, Error: err}:
			case <-ctx.Done():
			}
			return
		}

		// Scan each repo for metadata in parallel
		scanned := s.scanRepos(ctx, repos, workspace.Name)

		// Update cache with results from this workspace
		s.mu.Lock()
		// Remove old repos from this workspace
		for path, repo := range s.cache.Repos {
			if repo.WorkspaceName == workspace.Name {
				delete(s.cache.Repos, path)
			}
		}
		// Add new repos from this workspace
		for _, repo := range scanned {
			s.cache.Repos[repo.Path] = repo
		}
		s.mu.Unlock()

		// Save cache to disk
		if err := s.cache.Save(); err != nil {
			select {
			case results <- ScanResult{Repos: scanned, Error: fmt.Errorf("failed to save cache: %v", err)}:
			case <-ctx.Done():
			}
			return
		}

		select {
		case results <- ScanResult{Repos: scanned, Error: nil}:
		case <-ctx.Done():
		}
	}()

	return results
}
// DiscoverReposIncremental quickly finds repos and reports them one by one
func (s *Scanner) DiscoverReposIncremental(ctx context.Context, workspace Workspace) <-chan RepoInfo {
	results := make(chan RepoInfo, 10) // Buffer for faster processing

	go func() {
		defer close(results)

		// Quick discovery - just find .git directories without deep scanning
		repoPaths, err := s.discoverReposQuick(ctx, workspace)
		if err != nil {
			return
		}

		// Send each discovered repo immediately with basic info
		for _, repoPath := range repoPaths {
			select {
			case <-ctx.Done():
				return
			default:
			}

			// Create basic repo info immediately
			repo := RepoInfo{
				Path:          repoPath,
				Name:          filepath.Base(repoPath),
				WorkspaceName: workspace.Name,
				LastScanned:   time.Now(),
				// Branch, Ahead, Behind will be filled in later
			}

			select {
			case results <- repo:
			case <-ctx.Done():
				return
			}

			// Optionally enrich with metadata in the background
			// This is done asynchronously so UI updates immediately
			go s.enrichRepoMetadata(ctx, &repo)
		}
	}()

	return results
}

// discoverReposQuick finds git repos without deep metadata scanning
func (s *Scanner) discoverReposQuick(ctx context.Context, workspace Workspace) ([]string, error) {
	var repos []string

	// First level scan - look at immediate subdirectories
	entries, err := os.ReadDir(workspace.Path)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		select {
		case <-ctx.Done():
			return repos, ctx.Err()
		default:
		}

		if !entry.IsDir() {
			continue
		}

		entryPath := filepath.Join(workspace.Path, entry.Name())

		// Skip hidden directories (except those that might contain repos)
		if strings.HasPrefix(entry.Name(), ".") && entry.Name() != ".git" {
			continue
		}

		// Check if this is a git repo
		gitDir := filepath.Join(entryPath, ".git")
		if _, err := os.Stat(gitDir); err == nil {
			repos = append(repos, entryPath)
			continue
		}

		// Check if it's a bare repo
		if _, err := os.Stat(filepath.Join(entryPath, "HEAD")); err == nil {
			if _, err := os.Stat(filepath.Join(entryPath, "refs")); err == nil {
				repos = append(repos, entryPath)
				continue
			}
		}

		// For non-git directories, do a shallow scan (one level down)
		// This catches common structures like ~/code/project1, ~/code/project2
		subEntries, err := os.ReadDir(entryPath)
		if err != nil {
			continue // Skip directories we can't read
		}

		for _, subEntry := range subEntries {
			if !subEntry.IsDir() {
				continue
			}

			subPath := filepath.Join(entryPath, subEntry.Name())

			// Skip common build/dependency directories
			if subEntry.Name() == "node_modules" || subEntry.Name() == "target" ||
			   subEntry.Name() == "build" || subEntry.Name() == "dist" {
				continue
			}

			// Check for git repo
			if _, err := os.Stat(filepath.Join(subPath, ".git")); err == nil {
				repos = append(repos, subPath)
			}
		}
	}

	return repos, nil
}

// enrichRepoMetadata fills in branch, ahead/behind info asynchronously
func (s *Scanner) enrichRepoMetadata(ctx context.Context, repo *RepoInfo) {
	select {
	case <-ctx.Done():
		return
	default:
	}

	// Get current branch
	if branch, err := git.GetCurrentBranch(repo.Path); err == nil {
		repo.Branch = branch
	}

	// Get ahead/behind info
	if ahead, behind, ok := git.GetAheadBehind(repo.Path); ok {
		repo.Ahead = ahead
		repo.Behind = behind
		repo.HasUpstream = true
	}

	// Get last commit time
	if commits, err := git.GetCommits(repo.Path, 1); err == nil && len(commits) > 0 {
		repo.LastCommitTime = commits[0].Time
	}

	// Update cache
	s.mu.Lock()
	s.cache.Repos[repo.Path] = *repo
	s.mu.Unlock()
}

// UpdateCacheRepo updates a single repo in cache (thread-safe)
func (s *Scanner) UpdateCacheRepo(repo RepoInfo) {
	s.mu.Lock()
	s.cache.Repos[repo.Path] = repo
	s.mu.Unlock()
}

// GetCache returns the cache (for saving to disk)
func (s *Scanner) GetCache() *RepoCache {
	return s.cache
}

// UpdateLastRepo updates the last accessed repository in cache
func (s *Scanner) UpdateLastRepo(repoPath string) {
	s.mu.Lock()
	s.cache.LastRepoPath = repoPath
	// Also update workspace if we can determine it from the repo
	if repo, exists := s.cache.Repos[repoPath]; exists {
		s.cache.LastWorkspace = repo.WorkspaceName
	}
	s.mu.Unlock()
}

// UpdateLastWorkspace updates the last accessed workspace in cache
func (s *Scanner) UpdateLastWorkspace(workspaceName string) {
	s.mu.Lock()
	s.cache.LastWorkspace = workspaceName
	s.mu.Unlock()
}
