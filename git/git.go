package git

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

type Repository struct {
	Path       string
	Name       string
	CurrentBranch string
}

func OpenRepository(path string) (*Repository, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = absPath
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("not a git repository: %w", err)
	}

	repoPath := strings.TrimSpace(string(output))
	
	branch, _ := getCurrentBranch(repoPath)
	
	return &Repository{
		Path:       repoPath,
		Name:       filepath.Base(repoPath),
		CurrentBranch: branch,
	}, nil
}

func getCurrentBranch(repoPath string) (string, error) {
	cmd := exec.Command("git", "branch", "--show-current")
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func GetCommits(repoPath string, limit int) ([]Commit, error) {
	format := "%H%x00%h%x00%an%x00%ae%x00%at%x00%s%x00%b%x00"
	cmd := exec.Command("git", "log", fmt.Sprintf("--max-count=%d", limit), "--format="+format)
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var commits []Commit
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\x00")
		if len(parts) >= 6 {
			commits = append(commits, Commit{
				Hash:      parts[0],
				ShortHash: parts[1],
				Author:    parts[2],
				Email:     parts[3],
				Date:      parts[4],
				Subject:   parts[5],
				Body:      func() string {
					if len(parts) > 6 {
						return parts[6]
					}
					return ""
				}(),
			})
		}
	}
	return commits, nil
}

type Commit struct {
	Hash      string
	ShortHash string
	Author    string
	Email     string
	Date      string
	Subject   string
	Body      string
}

func GetBranches(repoPath string) ([]Branch, error) {
	cmd := exec.Command("git", "branch", "-a", "--format=%(refname:short)%x00%(HEAD)")
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var branches []Branch
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\x00")
		if len(parts) >= 2 {
			branches = append(branches, Branch{
				Name:      parts[0],
				IsCurrent: parts[1] == "*",
			})
		}
	}
	return branches, nil
}

type Branch struct {
	Name      string
	IsCurrent bool
}