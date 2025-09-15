package git

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
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
	// %x1e = RS between commits, %x00 between fields
	const logFmt = "%H%x00%h%x00%an%x00%ae%x00%at%x00%s%x00%b%x00%x1e"

	cmd := exec.Command("git", "log", fmt.Sprintf("--max-count=%d", limit), "--format="+logFmt)
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	out := string(output)
	recs := strings.Split(strings.TrimSuffix(out, "\x1e"), "\x1e")
	commits := make([]Commit, 0, len(recs))

	for _, r := range recs {
		if r == "" {
			continue
		}
		p := strings.Split(r, "\x00")
		if len(p) < 6 {
			continue
		}

		ts, _ := strconv.ParseInt(p[4], 10, 64)
		commits = append(commits, Commit{
			Hash:      p[0],
			ShortHash: p[1],
			Author:    p[2],
			Email:     p[3],
			Date:      p[4],
			Time:      time.Unix(ts, 0),
			Subject:   p[5],
			Body:      strings.Join(p[6:], "\x00"), // body itself may have \x00 if you ever add more fields; safe join
		})
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
	// Use porcelain v2 with NUL-separated output for robust parsing
	cmd := exec.Command("git", "status", "--porcelain=v2", "-z")
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	status := &Status{
		Files: []FileStatus{},
	}

	b := output
	for len(b) > 0 {
		// Each record starts with a code, ends with NUL(s)
		// v2 formats:
		// 1 <xy> <sub> <mH> <mI> <mW> <hH> <hI> <path>\0
		// 2 <xy> <sub> <mH> <mI> <mW> <hH> <hI> <X><score> <path>\0<origPath>\0
		// ? <path>\0
		// ! <path>\0 (ignored file)

		if len(b) == 0 {
			break
		}

		switch b[0] {
		case '?':
			// Untracked file
			b = b[2:] // skip "? "
			path, rest := readToNul(b)
			status.Files = append(status.Files, FileStatus{
				Path:     string(path),
				Unstaged: "untracked",
			})
			b = rest

		case '!':
			// Ignored file - skip it
			_, rest := readToNul(b)
			b = rest

		case '1':
			// Normal change
			line, rest := readToNul(b)
			fields := strings.Fields(string(line))
			if len(fields) < 9 {
				b = rest
				continue
			}

			xy := fields[1]
			path := fields[8]

			fileStatus := FileStatus{Path: path}

			// Parse staged status (X)
			switch xy[0] {
			case 'M':
				fileStatus.Staged = "modified"
			case 'A':
				fileStatus.Staged = "added"
			case 'D':
				fileStatus.Staged = "deleted"
			case 'R':
				fileStatus.Staged = "renamed"
			case 'C':
				fileStatus.Staged = "copied"
			}

			// Parse unstaged status (Y)
			switch xy[1] {
			case 'M':
				fileStatus.Unstaged = "modified"
			case 'D':
				fileStatus.Unstaged = "deleted"
			}

			status.Files = append(status.Files, fileStatus)
			b = rest

		case '2':
			// Rename or copy
			line, rest := readToNul(b)
			fields := strings.Fields(string(line))
			if len(fields) < 10 {
				b = rest
				continue
			}

			xy := fields[1]
			// After the first NUL, read the two paths
			origPath, rest2 := readToNul(rest)
			newPath, rest3 := readToNul(rest2)

			fileStatus := FileStatus{
				Path:    string(newPath),
				OldPath: string(origPath),
			}

			// Parse staged status (X)
			switch xy[0] {
			case 'R':
				fileStatus.Staged = "renamed"
			case 'C':
				fileStatus.Staged = "copied"
			}

			// Parse unstaged status (Y)
			switch xy[1] {
			case 'M':
				fileStatus.Unstaged = "modified"
			case 'D':
				fileStatus.Unstaged = "deleted"
			}

			status.Files = append(status.Files, fileStatus)
			b = rest3

		default:
			// Unknown format, skip to next NUL
			_, rest := readToNul(b)
			b = rest
		}
	}

	return status, nil
}

func readToNul(b []byte) (field []byte, rest []byte) {
	i := bytes.IndexByte(b, 0)
	if i < 0 {
		return b, nil
	}
	return b[:i], b[i+1:]
}

type Status struct {
	Files []FileStatus
}

type FileStatus struct {
	Path     string
	OldPath  string // For renames
	Staged   string
	Unstaged string
}

func GetDiff(repoPath string, path string, staged bool) (string, error) {
	args := []string{"-c", "color.ui=false", "diff", "--no-ext-diff", "-U3"}
	if staged {
		args = append(args, "--cached")
	}
	if path != "" {
		args = append(args, "--", path)
	}

	return run(repoPath, args...)
}

type Numstat struct {
	Added   string // "-" means binary
	Deleted string // "-" means binary
	OldPath string // for renames, this is the old path
	Path    string // current path
}

func DiffNumstat(repoPath string, staged bool, paths ...string) ([]Numstat, error) {
	args := []string{"diff", "--numstat", "--no-textconv"}
	if staged {
		args = append(args, "--cached")
	}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}

	cmd := exec.Command("git", args...)
	cmd.Dir = repoPath
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return nil, err
	}

	var res []Numstat
	sc := bufio.NewScanner(strings.NewReader(out.String()))
	for sc.Scan() {
		// formats:
		// "12\t3\tpath"
		// "-\t-\tpath"                              (binary)
		// "10\t2\toldpath\tnewpath"                 (rename)
		fields := strings.Split(sc.Text(), "\t")
		if len(fields) < 3 {
			continue
		}
		n := Numstat{Added: fields[0], Deleted: fields[1]}
		if len(fields) == 3 {
			n.Path = fields[2]
		} else {
			n.OldPath = fields[2]
			n.Path = fields[3]
		}
		res = append(res, n)
	}
	return res, sc.Err()
}

func IsBinaryChange(repoPath string, staged bool, path string) (bool, error) {
	stats, err := DiffNumstat(repoPath, staged, path)
	if err != nil {
		return false, err
	}
	if len(stats) == 0 {
		return false, nil // no change
	}
	return stats[0].Added == "-" && stats[0].Deleted == "-", nil
}

func GetFileContents(repoPath string, path string) (string, error) {
	fullPath := filepath.Join(repoPath, path)
	content, err := os.ReadFile(fullPath)
	if err != nil {
		return "", err
	}
	return string(content), nil
}

func IsBinaryFile(repoPath string, path string) bool {
	fullPath := filepath.Join(repoPath, path)

	// Read first 512 bytes to check for binary content
	file, err := os.Open(fullPath)
	if err != nil {
		return false
	}
	defer file.Close()

	buffer := make([]byte, 512)
	n, err := file.Read(buffer)
	if err != nil && n == 0 {
		return false
	}

	// Check for null bytes (common indicator of binary files)
	for i := 0; i < n; i++ {
		if buffer[i] == 0 {
			return true
		}
	}

	// Check for high percentage of non-printable characters
	nonPrintable := 0
	for i := 0; i < n; i++ {
		// Allow common whitespace characters: tab(9), LF(10), CR(13), and space(32)
		// Also allow printable ASCII (32-126) and common extended ASCII
		if buffer[i] < 9 || (buffer[i] > 13 && buffer[i] < 32) || buffer[i] > 126 {
			nonPrintable++
		}
	}

	// If more than 30% non-printable, consider it binary
	return float64(nonPrintable)/float64(n) > 0.3
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

// run executes a git command and returns the output
func run(repoPath string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if repoPath != "" {
		cmd.Dir = repoPath
	}
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(output), nil
}

// UntrackedIsBinary detects if an untracked file is binary using git diff --numstat
func UntrackedIsBinary(repoPath, rel string) (bool, error) {
	abs := filepath.Join(repoPath, rel)

	cmd := exec.Command("git", "diff", "--numstat", "--no-textconv", "--no-index", "--", "/dev/null", abs)
	output, err := cmd.Output()
	// git diff exits with code 1 when there are differences, which is expected
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			// Exit code 1 is expected for git diff when files differ
		} else {
			return false, err
		}
	}

	out := string(output)
	// lines look like "-\t-\t/path" for binary
	for _, ln := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Split(ln, "\t")
		if len(f) >= 2 {
			return f[0] == "-" && f[1] == "-", nil
		}
	}
	return false, nil
}

// UntrackedPatch generates a patch for an untracked file using git diff --no-index
func UntrackedPatch(repoPath, rel string) (string, error) {
	abs := filepath.Join(repoPath, rel)

	cmd := exec.Command("git", "diff", "--no-index", "--", "/dev/null", abs)
	output, err := cmd.Output()
	// git diff exits with code 1 when there are differences, which is expected
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			// Exit code 1 is expected for git diff when files differ
		} else {
			return "", err
		}
	}

	return string(output), nil
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