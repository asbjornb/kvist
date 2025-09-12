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
	leftTopPanel panel = iota    // branches
	leftBottomPanel             // remotes/stash/repo info  
	rightTopPanel               // commits or files (based on mode)
	rightBottomPanel            // details/diff
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
}

func initialModel() model {
	return model{
		activePanel: rightTopPanel,
		currentMode: historyMode,
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
			m.activePanel = (m.activePanel + 1) % 4
		case "up", "k":
			switch m.activePanel {
			case rightTopPanel:
				if m.currentMode == historyMode {
					if m.selectedCommit > 0 {
						m.selectedCommit--
					}
				} else if m.currentMode == filesMode {
					if m.selectedFile > 0 {
						m.selectedFile--
					}
				}
			case leftTopPanel:
				if m.selectedBranch > 0 {
					m.selectedBranch--
				}
			}
		case "down", "j":
			switch m.activePanel {
			case rightTopPanel:
				if m.currentMode == historyMode {
					if m.selectedCommit < len(m.commits)-1 {
						m.selectedCommit++
					}
				} else if m.currentMode == filesMode {
					if m.status != nil && m.selectedFile < len(m.status.Files)-1 {
						m.selectedFile++
					}
				}
			case leftTopPanel:
				if m.selectedBranch < len(m.branches)-1 {
					m.selectedBranch++
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
		case " ", "enter":
			if m.activePanel == rightTopPanel && m.currentMode == filesMode && m.status != nil && m.selectedFile < len(m.status.Files) {
				file := m.status.Files[m.selectedFile]
				if file.Staged != "" {
					return m, doFileOperation(m.repo.Path, file.Path, "unstage")
				} else {
					return m, doFileOperation(m.repo.Path, file.Path, "stage")
				}
			} else if m.activePanel == leftTopPanel && m.selectedBranch < len(m.branches) {
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
	// 2x2 grid layout - give more space to right pane
	leftWidth := m.width / 3
	rightWidth := m.width - leftWidth
	topHeight := height * 2 / 3
	bottomHeight := height - topHeight

	// Left column
	leftTop := m.renderLeftTop(leftWidth, topHeight)
	leftBottom := m.renderLeftBottom(leftWidth, bottomHeight)
	leftColumn := lipgloss.JoinVertical(lipgloss.Top, leftTop, leftBottom)

	// Right column - content depends on current mode
	var rightTop, rightBottom string
	if m.currentMode == historyMode {
		rightTop = m.renderCommits(rightWidth, topHeight)
		rightBottom = m.renderCommitDetails(rightWidth, bottomHeight)
	} else { // filesMode
		rightTop = m.renderFiles(rightWidth, topHeight)
		rightBottom = m.renderFileDiff(rightWidth, bottomHeight)
	}
	rightColumn := lipgloss.JoinVertical(lipgloss.Top, rightTop, rightBottom)

	return lipgloss.JoinHorizontal(lipgloss.Top, leftColumn, rightColumn)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (m model) renderLeftTop(width, height int) string {
	panelStyle := lipgloss.NewStyle().
		Width(width).
		Height(height).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(func() string {
			if m.activePanel == leftTopPanel {
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
		if m.activePanel == leftTopPanel && i == m.selectedBranch {
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

func (m model) renderLeftBottom(width, height int) string {
	panelStyle := lipgloss.NewStyle().
		Width(width).
		Height(height).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(func() string {
			if m.activePanel == leftBottomPanel {
				return "170"
			}
			return "240"
		}()))

	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("170"))

	title := titleStyle.Render("Info")
	content := []string{title, ""}

	if m.repo != nil {
		// Show remotes
		if len(m.remotes) > 0 {
			remoteStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
			content = append(content, remoteStyle.Render("Remotes:"))
			for i, remote := range m.remotes {
				if i >= 3 { // Limit to 3 remotes to save space
					content = append(content, "  ...")
					break
				}
				remoteName := fmt.Sprintf("  %s", remote.Name)
				if len(remoteName) > width-4 {
					remoteName = remoteName[:width-7] + "..."
				}
				content = append(content, remoteName)
			}
		}
		
		// Show stashes
		if len(m.stashes) > 0 {
			if len(m.remotes) > 0 {
				content = append(content, "")
			}
			stashStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("170"))
			content = append(content, stashStyle.Render("Stashes:"))
			for i, stash := range m.stashes {
				if i >= 3 { // Limit to 3 stashes to save space
					content = append(content, "  ...")
					break
				}
				stashText := fmt.Sprintf("  %s", stash.Index)
				if len(stashText) > width-4 {
					stashText = stashText[:width-7] + "..."
				}
				content = append(content, stashText)
			}
		}
		
		// Show basic stats if space allows
		if len(content) < height-5 {
			content = append(content, "")
			statsStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("242"))
			content = append(content, statsStyle.Render("Stats:"))
			content = append(content, fmt.Sprintf("  %d commits", len(m.commits)))
			content = append(content, fmt.Sprintf("  %d branches", len(m.branches)))
			if m.status != nil && len(m.status.Files) > 0 {
				content = append(content, fmt.Sprintf("  %d changes", len(m.status.Files)))
			}
		}
	} else {
		content = append(content, "No repository loaded")
	}

	return panelStyle.Render(strings.Join(content, "\n"))
}

func (m model) renderCommits(width, height int) string {
	panelStyle := lipgloss.NewStyle().
		Width(width).
		Height(height).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(func() string {
			if m.activePanel == rightTopPanel {
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
		if m.activePanel == rightTopPanel && i == m.selectedCommit {
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
			if m.activePanel == rightTopPanel && m.currentMode == filesMode {
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
			if m.activePanel == rightTopPanel && m.currentMode == filesMode && i == m.selectedFile {
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
			if m.activePanel == rightBottomPanel {
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
			if m.activePanel == rightBottomPanel {
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

	var helpLines []string
	if m.width < 80 {
		// Compact help for narrow terminals
		helpLines = []string{
			"tab: panels • ↑↓/jk: nav • space: stage • h: history • s: files",
			"f: fetch • p: pull • P: push • r: refresh • q: quit",
		}
	} else {
		helpLines = []string{
			"tab: switch panel • ↑↓/jk: navigate • space/enter: stage/checkout",
			"h: history mode • s: files mode • f: fetch • p: pull • P: push • r: refresh • q: quit",
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