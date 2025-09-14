package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/asbjornb/kvist/git"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type panel int

const (
	topPanel panel = iota    // commits or files (based on mode)
	bottomPanel             // details/diff
)

type viewMode int

const (
	historyMode viewMode = iota  // showing commits + details
	filesMode                   // showing files + diff
)

type model struct {
	width      int
	height     int
	ready      bool
	repo       *git.Repository
	commits    []git.Commit
	branches   []git.Branch
	status     *git.Status
	remotes    []git.Remote
	stashes    []git.Stash
	activePanel panel
	currentMode viewMode
	selectedCommit int
	selectedBranch int
	selectedFile   int
	err        error
	// Branch creation state
	creatingBranch bool
	branchInput    string
}

func initialModel() model {
	return model{
		activePanel: topPanel,
		currentMode: filesMode,
	}
}

func (m model) Init() tea.Cmd {
	return loadRepository(".")
}

type repoLoadedMsg struct {
	repo     *git.Repository
	commits  []git.Commit
	branches []git.Branch
	status   *git.Status
	remotes  []git.Remote
	stashes  []git.Stash
	err      error
}

func loadRepository(path string) tea.Cmd {
	return func() tea.Msg {
		repo, err := git.OpenRepository(path)
		if err != nil {
			return repoLoadedMsg{err: err}
		}
		
		commits, _ := git.GetCommits(repo.Path, 50)
		branches, _ := git.GetBranches(repo.Path)
		status, _ := git.GetStatus(repo.Path)
		remotes, _ := git.GetRemotes(repo.Path)
		stashes, _ := git.GetStashes(repo.Path)
		
		return repoLoadedMsg{
			repo:     repo,
			commits:  commits,
			branches: branches,
			status:   status,
			remotes:  remotes,
			stashes:  stashes,
		}
	}
}

type gitOperationMsg struct {
	operation string
	err       error
}

func doGitOperation(repoPath string, operation string) tea.Cmd {
	return func() tea.Msg {
		var err error
		switch operation {
		case "fetch":
			err = git.Fetch(repoPath)
		case "pull":
			err = git.Pull(repoPath)
		case "push":
			err = git.Push(repoPath)
		}
		return gitOperationMsg{operation: operation, err: err}
	}
}

type fileOperationMsg struct {
	operation string
	path      string
	err       error
}

func doFileOperation(repoPath string, path string, operation string) tea.Cmd {
	return func() tea.Msg {
		var err error
		switch operation {
		case "stage":
			err = git.StageFile(repoPath, path)
		case "unstage":
			err = git.UnstageFile(repoPath, path)
		}
		return fileOperationMsg{operation: operation, path: path, err: err}
	}
}

type branchOperationMsg struct {
	operation string
	branch    string
	err       error
}

func doBranchOperation(repoPath string, branch string, operation string) tea.Cmd {
	return func() tea.Msg {
		var err error
		switch operation {
		case "checkout":
			err = git.CheckoutBranch(repoPath, branch)
		case "create":
			err = git.CreateBranch(repoPath, branch)
		}
		return branchOperationMsg{operation: operation, branch: branch, err: err}
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Handle branch creation input
		if m.creatingBranch {
			switch msg.String() {
			case "ctrl+c", "esc":
				m.creatingBranch = false
				m.branchInput = ""
			case "enter":
				if m.branchInput != "" && m.repo != nil {
					m.creatingBranch = false
					branchName := m.branchInput
					m.branchInput = ""
					return m, doBranchOperation(m.repo.Path, branchName, "create")
				}
			case "backspace":
				if len(m.branchInput) > 0 {
					m.branchInput = m.branchInput[:len(m.branchInput)-1]
				}
			default:
				// Add printable characters to branch name
				if len(msg.String()) == 1 && msg.String()[0] >= 32 && msg.String()[0] <= 126 {
					m.branchInput += msg.String()
				}
			}
			return m, nil
		}
		
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "tab":
			m.activePanel = (m.activePanel + 1) % 2
		case "up", "k":
			switch m.activePanel {
			case topPanel:
				if m.currentMode == historyMode {
					if m.selectedCommit > 0 {
						m.selectedCommit--
					}
				} else if m.currentMode == filesMode {
					if m.selectedFile > 0 {
						m.selectedFile--
					}
				}
			}
		case "down", "j":
			switch m.activePanel {
			case topPanel:
				if m.currentMode == historyMode {
					if m.selectedCommit < len(m.commits)-1 {
						m.selectedCommit++
					}
				} else if m.currentMode == filesMode {
					if m.status != nil && m.selectedFile < len(m.status.Files)-1 {
						m.selectedFile++
					}
				}
			}
		case "f":
			if m.repo != nil {
				return m, doGitOperation(m.repo.Path, "fetch")
			}
		case "p":
			if m.repo != nil {
				return m, doGitOperation(m.repo.Path, "pull")
			}
		case "P":
			if m.repo != nil {
				return m, doGitOperation(m.repo.Path, "push")
			}
		case "r":
			return m, loadRepository(".")
		case "h":
			m.currentMode = historyMode
		case "s":
			m.currentMode = filesMode
		case "n":
			if m.repo != nil {
				m.creatingBranch = true
				m.branchInput = ""
			}
		case " ", "enter":
			if m.activePanel == topPanel && m.currentMode == filesMode && m.status != nil && m.selectedFile < len(m.status.Files) {
				file := m.status.Files[m.selectedFile]
				if file.Staged != "" {
					return m, doFileOperation(m.repo.Path, file.Path, "unstage")
				} else {
					return m, doFileOperation(m.repo.Path, file.Path, "stage")
				}
			}
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if !m.ready {
			m.ready = true
		}
	case repoLoadedMsg:
		m.repo = msg.repo
		m.commits = msg.commits
		m.branches = msg.branches
		m.status = msg.status
		m.remotes = msg.remotes
		m.stashes = msg.stashes
		m.err = msg.err
	case gitOperationMsg:
		if msg.err == nil {
			return m, loadRepository(".")
		}
	case fileOperationMsg:
		if msg.err == nil {
			return m, loadRepository(".")
		}
	case branchOperationMsg:
		if msg.err == nil {
			return m, loadRepository(".")
		}
	}
	return m, nil
}

func (m model) View() string {
	if !m.ready {
		return "\n  Initializing..."
	}

	if m.err != nil {
		return fmt.Sprintf("\n  Error: %v\n\n  Make sure you're in a git repository.\n", m.err)
	}

	if m.repo == nil {
		return "\n  Loading repository..."
	}

	headerHeight := 3
	helpHeight := 4
	contentHeight := m.height - headerHeight - helpHeight

	header := m.renderHeader()
	content := m.renderContent(contentHeight)
	help := m.renderHelp()

	result := lipgloss.JoinVertical(lipgloss.Top, header, content, help)
	
	// Show branch creation prompt overlay
	if m.creatingBranch {
		promptStyle := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("170")).
			Background(lipgloss.Color("235")).
			Padding(1).
			Margin(1)
		
		prompt := fmt.Sprintf("Create new branch: %s█", m.branchInput)
		promptHelp := "Enter: create • Esc: cancel"
		
		overlay := promptStyle.Render(prompt + "\n" + promptHelp)
		
		// Position overlay in center
		overlayHeight := 5
		overlayTop := (m.height - overlayHeight) / 2
		
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Top, result) +
			lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Top, 
				strings.Repeat("\n", overlayTop) + overlay)
	}

	return result
}

func (m model) renderHeader() string {
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("170")).
		MarginLeft(2)

	branchStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("214")).
		MarginLeft(2)

	title := titleStyle.Render("Kvist")
	repo := ""
	mode := ""
	if m.repo != nil {
		repo = fmt.Sprintf("📁 %s  🌿 %s", m.repo.Name, m.repo.CurrentBranch)
		if m.currentMode == historyMode {
			mode = "  [History Mode]"
		} else {
			mode = "  [Files Mode]"
		}
	}
	repoInfo := branchStyle.Render(repo + mode)

	return lipgloss.JoinVertical(lipgloss.Top, title, repoInfo, "")
}

func (m model) renderContent(height int) string {
	// Simple two-panel vertical layout
	topHeight := height * 2 / 3
	bottomHeight := height - topHeight

	// Content depends on current mode
	var top, bottom string
	if m.currentMode == historyMode {
		top = m.renderCommits(m.width, topHeight)
		bottom = m.renderCommitDetails(m.width, bottomHeight)
	} else { // filesMode
		top = m.renderFiles(m.width, topHeight)
		bottom = m.renderFileDiff(m.width, bottomHeight)
	}

	return lipgloss.JoinVertical(lipgloss.Top, top, bottom)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}


func (m model) renderCommits(width, height int) string {
	panelStyle := lipgloss.NewStyle().
		Width(width).
		Height(height).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(func() string {
			if m.activePanel == topPanel {
				return "170"
			}
			return "240"
		}()))

	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("170"))

	hashStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("214"))

	itemStyle := lipgloss.NewStyle().
		PaddingLeft(1)

	selectedStyle := lipgloss.NewStyle().
		PaddingLeft(1).
		Background(lipgloss.Color("238"))

	title := titleStyle.Render(func() string {
		if m.currentMode == historyMode {
			return "History"
		}
		return "Commits"
	}())
	content := []string{title, ""}

	for i, commit := range m.commits {
		if i >= height-3 {
			break
		}
		
		style := itemStyle
		if m.activePanel == topPanel && i == m.selectedCommit {
			style = selectedStyle
		}
		
		timeStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("242"))
		
		hash := hashStyle.Render(commit.ShortHash)
		relativeTime := git.FormatRelativeTime(commit.Time)
		timeText := timeStyle.Render(relativeTime)
		
		// Calculate available space for subject
		prefixLen := len(commit.ShortHash) + len(relativeTime) + 4 // spaces and separators
		maxSubjectLen := width - prefixLen - 4
		
		subject := commit.Subject
		if len(subject) > maxSubjectLen && maxSubjectLen > 3 {
			subject = subject[:maxSubjectLen-3] + "..."
		}
		
		line := fmt.Sprintf("%s %s %s", hash, timeText, subject)
		content = append(content, style.Width(width-2).Render(line))
	}

	return panelStyle.Render(strings.Join(content, "\n"))
}

func (m model) renderFiles(width, height int) string {
	panelStyle := lipgloss.NewStyle().
		Width(width).
		Height(height).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(func() string {
			if m.activePanel == topPanel && m.currentMode == filesMode {
				return "170"
			}
			return "240"
		}()))

	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("170"))

	itemStyle := lipgloss.NewStyle().
		PaddingLeft(1)

	selectedStyle := lipgloss.NewStyle().
		PaddingLeft(1).
		Background(lipgloss.Color("238"))

	stagedStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("42"))

	unstagedStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("214"))

	untrackedStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("241"))

	title := titleStyle.Render("Files")
	content := []string{title, ""}

	if m.status == nil || len(m.status.Files) == 0 {
		content = append(content, "  No changes")
	} else {
		for i, file := range m.status.Files {
			if i >= height-3 {
				break
			}

			style := itemStyle
			if m.activePanel == topPanel && m.currentMode == filesMode && i == m.selectedFile {
				style = selectedStyle
			}

			var statusChar string
			var statusStyle lipgloss.Style
			
			if file.Staged != "" {
				switch file.Staged {
				case "added":
					statusChar = "A"
					statusStyle = stagedStyle
				case "modified":
					statusChar = "M"
					statusStyle = stagedStyle
				case "deleted":
					statusChar = "D"
					statusStyle = stagedStyle
				case "renamed":
					statusChar = "R"
					statusStyle = stagedStyle
				}
			} else if file.Unstaged != "" {
				switch file.Unstaged {
				case "modified":
					statusChar = "M"
					statusStyle = unstagedStyle
				case "deleted":
					statusChar = "D"
					statusStyle = unstagedStyle
				case "untracked":
					statusChar = "?"
					statusStyle = untrackedStyle
				}
			}

			status := statusStyle.Render(statusChar)
			fileName := file.Path
			if len(fileName) > width-8 {
				fileName = "..." + fileName[len(fileName)-(width-11):]
			}
			
			line := fmt.Sprintf(" %s %s", status, fileName)
			content = append(content, style.Width(width-2).Render(line))
		}
	}

	return panelStyle.Render(strings.Join(content, "\n"))
}

func (m model) renderFileDiff(width, height int) string {
	panelStyle := lipgloss.NewStyle().
		Width(width).
		Height(height).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(func() string {
			if m.activePanel == bottomPanel {
				return "170"
			}
			return "240"
		}()))

	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("170"))

	title := titleStyle.Render("Diff")

	if m.status == nil || len(m.status.Files) == 0 || m.selectedFile >= len(m.status.Files) {
		content := title + "\n\n" + "  No file selected"
		return panelStyle.Render(content)
	}

	file := m.status.Files[m.selectedFile]
	content := []string{
		title,
		"",
		"File: " + file.Path,
		"Status: " + func() string {
			if file.Staged != "" && file.Unstaged != "" {
				return file.Staged + " (staged), " + file.Unstaged + " (unstaged)"
			} else if file.Staged != "" {
				return file.Staged + " (staged)"
			} else if file.Unstaged != "" {
				return file.Unstaged
			}
			return "unknown"
		}(),
		"",
		"Diff preview coming soon...",
	}

	return panelStyle.Render(strings.Join(content, "\n"))
}

func (m model) renderCommitDetails(width, height int) string {
	panelStyle := lipgloss.NewStyle().
		Width(width).
		Height(height).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(func() string {
			if m.activePanel == bottomPanel {
				return "170"
			}
			return "240"
		}()))

	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("170"))

	hashStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("214"))

	authorStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("242"))

	title := titleStyle.Render("Details")

	if len(m.commits) == 0 || m.selectedCommit >= len(m.commits) {
		content := title + "\n\n" + "  No commit selected"
		return panelStyle.Render(content)
	}

	commit := m.commits[m.selectedCommit]
	
	timeStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("114"))
	
	content := []string{
		title,
		"",
		hashStyle.Render("Hash: " + commit.ShortHash),
		authorStyle.Render("Author: " + commit.Author),
		timeStyle.Render("Date: " + commit.Time.Format("2006-01-02 15:04:05") + " (" + git.FormatRelativeTime(commit.Time) + ")"),
		"",
		"Subject:",
		lipgloss.NewStyle().PaddingLeft(2).Render(commit.Subject),
	}

	if commit.Body != "" && strings.TrimSpace(commit.Body) != "" {
		content = append(content, "", "Body:")
		bodyLines := strings.Split(strings.TrimSpace(commit.Body), "\n")
		for _, line := range bodyLines {
			if len(content) >= height-3 {
				break
			}
			content = append(content, lipgloss.NewStyle().PaddingLeft(2).Width(width-4).Render(line))
		}
	}

	return panelStyle.Render(strings.Join(content, "\n"))
}

func (m model) renderHelp() string {
	helpStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("241")).
		MarginLeft(2)

	var helpLines []string
	if m.width < 80 {
		// Compact help for narrow terminals
		helpLines = []string{
			"tab: panels • ↑↓/jk: nav • space: stage • h: history • s: files",
			"n: new branch • f: fetch • p: pull • P: push • r: refresh • q: quit",
		}
	} else {
		helpLines = []string{
			"tab: switch panel • ↑↓/jk: navigate • space/enter: stage/checkout",
			"h: history mode • s: files mode • n: new branch • f: fetch • p: pull • P: push • r: refresh • q: quit",
		}
	}
	
	return helpStyle.Render(strings.Join(helpLines, "\n"))
}

func main() {
	p := tea.NewProgram(initialModel(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error running program: %v", err)
		os.Exit(1)
	}
}