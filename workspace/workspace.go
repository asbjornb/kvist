package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	ConfigVersion = 1
	ConfigDir     = ".config/kvist"
	CacheDir      = ".cache/kvist"
	ConfigFile    = "config.yaml"
	CacheFile     = "repos.json"
)

// Config represents the kvist configuration
type Config struct {
	Version    int         `yaml:"version"`
	Workspaces []Workspace `yaml:"workspaces"`
}

// Workspace represents a workspace configuration
type Workspace struct {
	Name string `yaml:"name"`
	Path string `yaml:"path"`
}

// RepoInfo holds metadata about a discovered repository
type RepoInfo struct {
	Path           string    `json:"path"`
	Name           string    `json:"name"`
	Branch         string    `json:"branch"`
	Ahead          int       `json:"ahead"`
	Behind         int       `json:"behind"`
	HasUpstream    bool      `json:"hasUpstream"`
	LastCommitTime time.Time `json:"lastCommitTime"`
	LastScanned    time.Time `json:"lastScanned"`
	WorkspaceName  string    `json:"workspaceName"`
}

// RepoCache holds cached repository information
type RepoCache struct {
	Version         time.Time           `json:"version"`
	Repos           map[string]RepoInfo `json:"repos"`           // path -> RepoInfo
	LastRepoPath    string              `json:"lastRepoPath"`    // last opened repository
	LastWorkspace   string              `json:"lastWorkspace"`   // last opened workspace
}

// LoadConfig loads the kvist configuration from disk
func LoadConfig() (*Config, error) {
	configPath := getConfigPath()

	// Create empty default config if doesn't exist
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		config := &Config{
			Version:    ConfigVersion,
			Workspaces: []Workspace{},
		}

		if err := config.Save(); err != nil {
			return nil, fmt.Errorf("failed to create default config: %w", err)
		}
		return config, nil
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	return &config, nil
}

// Save saves the configuration to disk
func (c *Config) Save() error {
	configPath := getConfigPath()

	// Ensure config directory exists
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// AddWorkspace adds a new workspace to the configuration
func (c *Config) AddWorkspace(name, path string) error {
	// Check if workspace with this name already exists
	for _, ws := range c.Workspaces {
		if ws.Name == name {
			return fmt.Errorf("workspace with name '%s' already exists", name)
		}
	}

	// Expand ~ to home directory
	expandedPath := ExpandPath(path)

	// Verify path exists and is a directory
	if stat, err := os.Stat(expandedPath); err != nil {
		return fmt.Errorf("path does not exist: %w", err)
	} else if !stat.IsDir() {
		return fmt.Errorf("path is not a directory: %s", expandedPath)
	}

	c.Workspaces = append(c.Workspaces, Workspace{
		Name: name,
		Path: expandedPath,
	})

	return c.Save()
}

// RemoveWorkspace removes a workspace from the configuration
func (c *Config) RemoveWorkspace(name string) error {
	for i, ws := range c.Workspaces {
		if ws.Name == name {
			c.Workspaces = append(c.Workspaces[:i], c.Workspaces[i+1:]...)
			return c.Save()
		}
	}
	return fmt.Errorf("workspace '%s' not found", name)
}

// LoadRepoCache loads cached repository information
func LoadRepoCache() (*RepoCache, error) {
	cachePath := getCachePath()

	if _, err := os.Stat(cachePath); os.IsNotExist(err) {
		return &RepoCache{
			Version: time.Now(),
			Repos:   make(map[string]RepoInfo),
		}, nil
	}

	data, err := os.ReadFile(cachePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read cache file: %w", err)
	}

	var cache RepoCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, fmt.Errorf("failed to parse cache file: %w", err)
	}

	return &cache, nil
}

// Save saves the repository cache to disk
func (rc *RepoCache) Save() error {
	cachePath := getCachePath()

	// Ensure cache directory exists
	if err := os.MkdirAll(filepath.Dir(cachePath), 0755); err != nil {
		return fmt.Errorf("failed to create cache directory: %w", err)
	}

	rc.Version = time.Now()

	data, err := json.MarshalIndent(rc, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal cache: %w", err)
	}

	if err := os.WriteFile(cachePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write cache file: %w", err)
	}

	return nil
}

// getConfigPath returns the full path to the config file
func getConfigPath() string {
	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, ConfigDir, ConfigFile)
}

// getCachePath returns the full path to the cache file
func getCachePath() string {
	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, CacheDir, CacheFile)
}

// ExpandPath expands ~ to the user's home directory
func ExpandPath(path string) string {
	if path == "" {
		return path
	}

	if path == "~" {
		homeDir, _ := os.UserHomeDir()
		return homeDir
	}

	if len(path) >= 2 && path[:2] == "~/" {
		homeDir, _ := os.UserHomeDir()
		return filepath.Join(homeDir, path[2:])
	}

	return path
}

// ListDirectories returns a list of directories in the given path
// Returns empty slice on error
func ListDirectories(path string) []string {
	expandedPath := ExpandPath(path)

	entries, err := os.ReadDir(expandedPath)
	if err != nil {
		return []string{}
	}

	var dirs []string
	for _, entry := range entries {
		if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
			dirs = append(dirs, entry.Name())
		}
	}

	return dirs
}

// GetDirectorySuggestions returns directory suggestions for autocomplete
// based on the current input path
func GetDirectorySuggestions(input string) []string {
	if input == "" {
		return []string{}
	}

	// Expand the path to get the actual filesystem path
	expandedPath := ExpandPath(input)

	// Get the directory to search in and the prefix to match
	dir := filepath.Dir(expandedPath)
	prefix := filepath.Base(expandedPath)

	// If input ends with /, we're looking for subdirectories
	if input[len(input)-1] == '/' {
		dir = expandedPath
		prefix = ""
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return []string{}
	}

	var suggestions []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		// Skip hidden directories
		if entry.Name()[0] == '.' {
			continue
		}

		// Check if name matches prefix
		if prefix == "" || strings.HasPrefix(strings.ToLower(entry.Name()), strings.ToLower(prefix)) {
			// Build the suggestion path using the original input format
			var suggestion string
			if input == "~/" || input == "~" {
				suggestion = "~/" + entry.Name()
			} else if strings.HasPrefix(input, "~/") {
				// Replace the last component with the matched entry
				basePath := filepath.Dir(input)
				if input[len(input)-1] == '/' {
					suggestion = input + entry.Name()
				} else {
					suggestion = basePath + "/" + entry.Name()
				}
			} else {
				// For absolute paths
				if input[len(input)-1] == '/' {
					suggestion = input + entry.Name()
				} else {
					suggestion = filepath.Join(filepath.Dir(input), entry.Name())
				}
			}
			suggestions = append(suggestions, suggestion)
		}
	}

	return suggestions
}