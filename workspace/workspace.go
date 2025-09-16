package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	Name    string `yaml:"name"`
	Path    string `yaml:"path"`
	Enabled bool   `yaml:"enabled"`
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
	Version time.Time           `json:"version"`
	Repos   map[string]RepoInfo `json:"repos"` // path -> RepoInfo
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

	// Verify path exists and is a directory
	if stat, err := os.Stat(path); err != nil {
		return fmt.Errorf("path does not exist: %w", err)
	} else if !stat.IsDir() {
		return fmt.Errorf("path is not a directory: %s", path)
	}

	c.Workspaces = append(c.Workspaces, Workspace{
		Name:    name,
		Path:    path,
		Enabled: true,
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