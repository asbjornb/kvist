package git

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
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
			timestamp, _ := strconv.ParseInt(parts[4], 10, 64)
			commitTime := time.Unix(timestamp, 0)
			
			commits = append(commits, Commit{
				Hash:      parts[0],
				ShortHash: parts[1],
				Author:    parts[2],
				Email:     parts[3],
				Date:      parts[4],
				Time:      commitTime,
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

func FormatRelativeTime(t time.Time) string {
	now := time.Now()
	diff := now.Sub(t)
	
	if diff < time.Minute {
		return "just now"
	} else if diff < time.Hour {
		minutes := int(diff.Minutes())
		if minutes == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", minutes)
	} else if diff < 24*time.Hour {
		hours := int(diff.Hours())
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	} else if diff < 7*24*time.Hour {
		days := int(diff.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	} else if diff < 30*24*time.Hour {
		weeks := int(diff.Hours() / 24 / 7)
		if weeks == 1 {
			return "1 week ago"
		}
		return fmt.Sprintf("%d weeks ago", weeks)
	} else if diff < 365*24*time.Hour {
		months := int(diff.Hours() / 24 / 30)
		if months == 1 {
			return "1 month ago"
		}
		return fmt.Sprintf("%d months ago", months)
	} else {
		years := int(diff.Hours() / 24 / 365)
		if years == 1 {
			return "1 year ago"
		}
		return fmt.Sprintf("%d years ago", years)
	}
}

type Commit struct {
	Hash      string
	ShortHash string
	Author    string
	Email     string
	Date      string
	Time      time.Time
	Subject   string
	Body      string
}

func GetBranches(repoPath string) ([]Branch, error) {
	cmd := exec.Command("git", "branch", "-a")
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var branches []Branch
	
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		
		var isCurrent bool
		var name string
		
		if strings.HasPrefix(line, "* ") {
			isCurrent = true
			name = strings.TrimSpace(line[2:])
		} else {
			isCurrent = false
			name = strings.TrimSpace(line)
		}
		
		// Skip remote tracking branches that are duplicates of local branches
		if strings.HasPrefix(name, "remotes/origin/") {
			remoteName := strings.TrimPrefix(name, "remotes/origin/")
			// Check if we already have this as a local branch
			hasLocal := false
			for _, existing := range branches {
				if existing.Name == remoteName {
					hasLocal = true
					break
				}
			}
			if hasLocal {
				continue
			}
			name = remoteName + " (remote)"
		}
		
		var ahead, behind int
		if isCurrent && !strings.Contains(name, "(remote)") {
			ahead, behind = getAheadBehind(repoPath, name)
		}
		
		branches = append(branches, Branch{
			Name:      name,
			IsCurrent: isCurrent,
			Ahead:     ahead,
			Behind:    behind,
		})
	}
	return branches, nil
}

func getAheadBehind(repoPath string, branch string) (int, int) {
	cmd := exec.Command("git", "rev-list", "--left-right", "--count", "origin/"+branch+"...HEAD")
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		return 0, 0
	}
	
	parts := strings.Fields(strings.TrimSpace(string(output)))
	if len(parts) >= 2 {
		behind := 0
		ahead := 0
		fmt.Sscanf(parts[0], "%d", &behind)
		fmt.Sscanf(parts[1], "%d", &ahead)
		return ahead, behind
	}
	return 0, 0
}

type Branch struct {
	Name      string
	IsCurrent bool
	Ahead     int
	Behind    int
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

func GetRemotes(repoPath string) ([]Remote, error) {
	cmd := exec.Command("git", "remote", "-v")
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var remotes []Remote
	remotesMap := make(map[string]*Remote)
	
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		
		parts := strings.Fields(line)
		if len(parts) >= 3 {
			name := parts[0]
			url := parts[1]
			direction := strings.Trim(parts[2], "()")
			
			if remote, exists := remotesMap[name]; exists {
				if direction == "push" {
					remote.PushURL = url
				}
			} else {
				remote := &Remote{
					Name: name,
				}
				if direction == "fetch" {
					remote.FetchURL = url
				} else {
					remote.PushURL = url
				}
				remotesMap[name] = remote
			}
		}
	}
	
	for _, remote := range remotesMap {
		remotes = append(remotes, *remote)
	}
	
	return remotes, nil
}

type Remote struct {
	Name     string
	FetchURL string
	PushURL  string
}

func GetStashes(repoPath string) ([]Stash, error) {
	cmd := exec.Command("git", "stash", "list", "--format=%gd%x00%gs%x00%gD")
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var stashes []Stash
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		
		parts := strings.Split(line, "\x00")
		if len(parts) >= 3 {
			stashes = append(stashes, Stash{
				Index:   parts[0],
				Message: parts[1],
				Date:    parts[2],
			})
		}
	}
	
	return stashes, nil
}

type Stash struct {
	Index   string
	Message string
	Date    string
}