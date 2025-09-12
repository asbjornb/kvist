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

func GetStatus(repoPath string) (*Status, error) {
	cmd := exec.Command("git", "status", "--porcelain=v1")
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	status := &Status{
		Files: []FileStatus{},
	}

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		if len(line) < 3 {
			continue
		}

		x := line[0]
		y := line[1]
		path := strings.TrimSpace(line[3:])

		var staged, unstaged string
		
		switch x {
		case 'M':
			staged = "modified"
		case 'A':
			staged = "added"
		case 'D':
			staged = "deleted"
		case 'R':
			staged = "renamed"
		case ' ', '?':
			staged = ""
		default:
			staged = "modified"
		}

		switch y {
		case 'M':
			unstaged = "modified"
		case 'D':
			unstaged = "deleted"
		case '?':
			unstaged = "untracked"
		case ' ':
			unstaged = ""
		default:
			unstaged = "modified"
		}

		status.Files = append(status.Files, FileStatus{
			Path:     path,
			Staged:   staged,
			Unstaged: unstaged,
		})
	}

	return status, nil
}

type Status struct {
	Files []FileStatus
}

type FileStatus struct {
	Path     string
	Staged   string
	Unstaged string
}

func GetDiff(repoPath string, path string, staged bool) (string, error) {
	args := []string{"diff"}
	if staged {
		args = append(args, "--cached")
	}
	if path != "" {
		args = append(args, "--", path)
	}
	
	cmd := exec.Command("git", args...)
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	
	return string(output), nil
}

func Fetch(repoPath string) error {
	cmd := exec.Command("git", "fetch")
	cmd.Dir = repoPath
	return cmd.Run()
}

func Pull(repoPath string) error {
	cmd := exec.Command("git", "pull")
	cmd.Dir = repoPath
	return cmd.Run()
}

func Push(repoPath string) error {
	cmd := exec.Command("git", "push")
	cmd.Dir = repoPath
	return cmd.Run()
}

func StageFile(repoPath string, path string) error {
	cmd := exec.Command("git", "add", path)
	cmd.Dir = repoPath
	return cmd.Run()
}

func UnstageFile(repoPath string, path string) error {
	cmd := exec.Command("git", "reset", "HEAD", path)
	cmd.Dir = repoPath
	return cmd.Run()
}

func CheckoutBranch(repoPath string, branch string) error {
	cmd := exec.Command("git", "checkout", branch)
	cmd.Dir = repoPath
	return cmd.Run()
}

func CreateBranch(repoPath string, branch string) error {
	cmd := exec.Command("git", "checkout", "-b", branch)
	cmd.Dir = repoPath
	return cmd.Run()
}

func DeleteBranch(repoPath string, branch string, force bool) error {
	args := []string{"branch"}
	if force {
		args = append(args, "-D")
	} else {
		args = append(args, "-d")
	}
	args = append(args, branch)
	
	cmd := exec.Command("git", args...)
	cmd.Dir = repoPath
	return cmd.Run()
}