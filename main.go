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
	branchPanel panel = iota
	commitPanel
	filesPanel
)

type model struct {
	width      int
	height     int
	ready      bool
	repo       *git.Repository
	commits    []git.Commit
	branches   []git.Branch
	status     *git.Status
	activePanel panel
	selectedCommit int
	selectedBranch int
	selectedFile   int
	err        error
}

func initialModel() model {
	return model{
		activePanel: commitPanel,
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
		
		return repoLoadedMsg{
			repo:     repo,
			commits:  commits,
			branches: branches,
			status:   status,
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
		}
		return branchOperationMsg{operation: operation, branch: branch, err: err}
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "tab":
			m.activePanel = (m.activePanel + 1) % 3
		case "up", "k":
			switch m.activePanel {
			case commitPanel:
				if m.selectedCommit > 0 {
					m.selectedCommit--
				}
			case branchPanel:
				if m.selectedBranch > 0 {
					m.selectedBranch--
				}
			case filesPanel:
				if m.selectedFile > 0 {
					m.selectedFile--
				}
			}
		case "down", "j":
			switch m.activePanel {
			case commitPanel:
				if m.selectedCommit < len(m.commits)-1 {
					m.selectedCommit++
				}
			case branchPanel:
				if m.selectedBranch < len(m.branches)-1 {
					m.selectedBranch++
				}
			case filesPanel:
				if m.status != nil && m.selectedFile < len(m.status.Files)-1 {
					m.selectedFile++
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
		case " ", "enter":
			if m.activePanel == filesPanel && m.status != nil && m.selectedFile < len(m.status.Files) {
				file := m.status.Files[m.selectedFile]
				if file.Staged != "" {
					return m, doFileOperation(m.repo.Path, file.Path, "unstage")
				} else {
					return m, doFileOperation(m.repo.Path, file.Path, "stage")
				}
			} else if m.activePanel == branchPanel && m.selectedBranch < len(m.branches) {
				branch := m.branches[m.selectedBranch]
				if !branch.IsCurrent {
					return m, doBranchOperation(m.repo.Path, branch.Name, "checkout")
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
	helpHeight := 2
	contentHeight := m.height - headerHeight - helpHeight

	header := m.renderHeader()
	content := m.renderContent(contentHeight)
	help := m.renderHelp()

	return lipgloss.JoinVertical(lipgloss.Top, header, content, help)
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
	if m.repo != nil {
		repo = fmt.Sprintf("📁 %s  🌿 %s", m.repo.Name, m.repo.CurrentBranch)
	}
	repoInfo := branchStyle.Render(repo)

	return lipgloss.JoinVertical(lipgloss.Top, title, repoInfo, "")
}

func (m model) renderContent(height int) string {
	// Responsive layout based on terminal width
	if m.width < 120 {
		// Narrow layout - stack vertically or reduce panels
		if m.width < 80 {
			// Very narrow - show only active panel
			switch m.activePanel {
			case branchPanel:
				return m.renderBranches(m.width, height)
			case commitPanel:
				return m.renderCommits(m.width, height)
			case filesPanel:
				return m.renderFiles(m.width, height)
			default:
				return m.renderCommits(m.width, height)
			}
		} else {
			// Medium width - show two panels side by side
			leftWidth := m.width / 2
			rightWidth := m.width - leftWidth
			
			switch m.activePanel {
			case branchPanel, commitPanel:
				branches := m.renderBranches(leftWidth, height)
				commits := m.renderCommits(rightWidth, height)
				return lipgloss.JoinHorizontal(lipgloss.Top, branches, commits)
			case filesPanel:
				files := m.renderFiles(leftWidth, height)
				details := m.renderCommitDetails(rightWidth, height)
				return lipgloss.JoinHorizontal(lipgloss.Top, files, details)
			default:
				branches := m.renderBranches(leftWidth, height)
				commits := m.renderCommits(rightWidth, height)
				return lipgloss.JoinHorizontal(lipgloss.Top, branches, commits)
			}
		}
	} else {
		// Wide layout - show all four panels
		branchWidth := max(20, m.width/5)
		commitWidth := max(30, m.width*2/5)
		filesWidth := max(20, m.width/5)
		detailWidth := m.width - branchWidth - commitWidth - filesWidth

		branches := m.renderBranches(branchWidth, height)
		commits := m.renderCommits(commitWidth, height)
		files := m.renderFiles(filesWidth, height)
		details := m.renderCommitDetails(detailWidth, height)

		return lipgloss.JoinHorizontal(lipgloss.Top, branches, commits, files, details)
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (m model) renderBranches(width, height int) string {
	panelStyle := lipgloss.NewStyle().
		Width(width).
		Height(height).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(func() string {
			if m.activePanel == branchPanel {
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
		Background(lipgloss.Color("238")).
		Foreground(lipgloss.Color("170"))

	title := titleStyle.Render("Branches")
	content := []string{title, ""}

	for i, branch := range m.branches {
		style := itemStyle
		if m.activePanel == branchPanel && i == m.selectedBranch {
			style = selectedStyle
		}
		
		prefix := "  "
		if branch.IsCurrent {
			prefix = "● "
		}
		
		branchText := branch.Name
		if branch.IsCurrent && (branch.Ahead > 0 || branch.Behind > 0) {
			indicators := ""
			if branch.Ahead > 0 {
				indicators += fmt.Sprintf("↑%d", branch.Ahead)
			}
			if branch.Behind > 0 {
				if indicators != "" {
					indicators += " "
				}
				indicators += fmt.Sprintf("↓%d", branch.Behind)
			}
			if indicators != "" {
				branchText += " " + indicators
			}
		}
		
		// Truncate if too long
		maxLen := width - 4
		if len(prefix + branchText) > maxLen {
			if maxLen > 3 {
				branchText = branchText[:maxLen-len(prefix)-3] + "..."
			}
		}
		
		line := style.Width(width - 2).Render(prefix + branchText)
		content = append(content, line)
	}

	return panelStyle.Render(strings.Join(content, "\n"))
}

func (m model) renderCommits(width, height int) string {
	panelStyle := lipgloss.NewStyle().
		Width(width).
		Height(height).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(func() string {
			if m.activePanel == commitPanel {
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

	title := titleStyle.Render("Commits")
	content := []string{title, ""}

	for i, commit := range m.commits {
		if i >= height-3 {
			break
		}
		
		style := itemStyle
		if m.activePanel == commitPanel && i == m.selectedCommit {
			style = selectedStyle
		}
		
		hash := hashStyle.Render(commit.ShortHash)
		subject := commit.Subject
		if len(subject) > width-15 {
			subject = subject[:width-18] + "..."
		}
		
		line := fmt.Sprintf("%s %s", hash, subject)
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
			if m.activePanel == filesPanel {
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
			if m.activePanel == filesPanel && i == m.selectedFile {
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

func (m model) renderCommitDetails(width, height int) string {
	panelStyle := lipgloss.NewStyle().
		Width(width).
		Height(height).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240"))

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
	
	content := []string{
		title,
		"",
		hashStyle.Render("Hash: " + commit.ShortHash),
		authorStyle.Render("Author: " + commit.Author),
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

	var help string
	if m.width < 80 {
		// Compact help for narrow terminals
		help = "tab: panels • ↑↓/jk: nav • space: stage • f: fetch • p: pull • P: push • q: quit"
	} else {
		help = "tab: switch panel • ↑↓/jk: navigate • space/enter: stage/checkout • f: fetch • p: pull • P: push • r: refresh • q: quit"
	}
	return helpStyle.Render(help)
}

func main() {
	p := tea.NewProgram(initialModel(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error running program: %v", err)
		os.Exit(1)
	}
}