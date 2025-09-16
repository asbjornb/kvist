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
	// Branch operations state
	showingBranchMenu bool
	creatingBranch    bool
	branchInput       string
	selectedBranchMenu int
	// Diff view state
	currentDiff    string
	diffScrollOffset int
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
	operation git.GitOp
	err       error
}

func doGitOperation(repoPath string, operation git.GitOp) tea.Cmd {
	return func() tea.Msg {
		err := git.ExecuteGitOp(repoPath, operation)
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

type diffLoadedMsg struct {
	diff string
	err  error
}

func loadDiff(repoPath string, filePath string, staged bool, isUntracked bool) tea.Cmd {
	return func() tea.Msg {
		if isUntracked {
			// Check if the file is binary using Git
			isBinary, err := git.UntrackedIsBinary(repoPath, filePath)
			if err != nil {
				return diffLoadedMsg{diff: "", err: err}
			}
			if isBinary {
				diff := fmt.Sprintf("Binary file %s (not shown)", filePath)
				return diffLoadedMsg{diff: diff, err: nil}
			}

			// For untracked text files, use Git to generate the patch
			diff, err := git.UntrackedPatch(repoPath, filePath)
			if err != nil {
				return diffLoadedMsg{diff: "", err: err}
			}

			return diffLoadedMsg{diff: diff, err: nil}
		}

		// For tracked files, first check if it's a binary change using numstat
		isBinary, err := git.IsBinaryChange(repoPath, staged, filePath)
		if err != nil {
			return diffLoadedMsg{diff: "", err: err}
		}

		if isBinary {
			diff := fmt.Sprintf("Binary file %s (not shown)", filePath)
			return diffLoadedMsg{diff: diff, err: nil}
		}

		// Get the actual diff for text files
		diff, err := git.GetDiff(repoPath, filePath, staged)
		if err != nil {
			return diffLoadedMsg{diff: "", err: err}
		}

		return diffLoadedMsg{diff: diff, err: nil}
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Handle branch operations
		if m.showingBranchMenu {
			switch msg.String() {
			case "ctrl+c", "esc":
				m.showingBranchMenu = false
				m.selectedBranchMenu = 0
			case "up", "k":
				if m.selectedBranchMenu > 0 {
					m.selectedBranchMenu--
				}
			case "down", "j":
				maxOptions := len(m.branches) + 1 // +1 for "Create new branch" option
				if m.selectedBranchMenu < maxOptions-1 {
					m.selectedBranchMenu++
				}
			case "enter":
				if m.selectedBranchMenu == 0 {
					// Create new branch option
					m.showingBranchMenu = false
					m.creatingBranch = true
					m.branchInput = ""
				} else {
					// Switch to selected branch
					branchIndex := m.selectedBranchMenu - 1
					if branchIndex < len(m.branches) {
						branch := m.branches[branchIndex]
						if !branch.IsCurrent && m.repo != nil {
							m.showingBranchMenu = false
							return m, doBranchOperation(m.repo.Path, branch.Name, "checkout")
						}
					}
				}
			}
			return m, nil
		}

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
						m.diffScrollOffset = 0
						if m.repo != nil && m.status != nil && m.selectedFile < len(m.status.Files) {
							file := m.status.Files[m.selectedFile]
							return m, loadDiff(m.repo.Path, file.Path, file.Staged != "", file.Unstaged == "untracked")
						}
					}
				}
			case bottomPanel:
				if m.currentMode == filesMode && m.diffScrollOffset > 0 {
					m.diffScrollOffset--
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
						m.diffScrollOffset = 0
						if m.repo != nil {
							file := m.status.Files[m.selectedFile]
							return m, loadDiff(m.repo.Path, file.Path, file.Staged != "", file.Unstaged == "untracked")
						}
					}
				}
			case bottomPanel:
				if m.currentMode == filesMode && m.currentDiff != "" {
					// Prevent scrolling beyond the content
					diffLines := strings.Split(m.currentDiff, "\n")
					maxScroll := len(diffLines) - 10 // Keep some buffer
					if maxScroll < 0 {
						maxScroll = 0
					}
					if m.diffScrollOffset < maxScroll {
						m.diffScrollOffset++
					}
				}
			}
		case "f":
			if m.repo != nil {
				return m, doGitOperation(m.repo.Path, git.OpFetch)
			}
		case "p":
			if m.repo != nil {
				return m, doGitOperation(m.repo.Path, git.OpPull)
			}
		case "P":
			if m.repo != nil {
				return m, doGitOperation(m.repo.Path, git.OpPush)
			}
		case "r":
			return m, loadRepository(".")
		case "h":
			m.currentMode = historyMode
			m.currentDiff = "" // Clear diff when switching to history mode
		case "s":
			m.currentMode = filesMode
			m.diffScrollOffset = 0 // Reset scroll when switching to files mode
		case "b":
			if m.repo != nil {
				m.showingBranchMenu = true
				m.selectedBranchMenu = 0
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
		// Load diff for first file if in files mode
		if m.currentMode == filesMode && m.repo != nil && m.status != nil && len(m.status.Files) > 0 {
			file := m.status.Files[0]
			return m, loadDiff(m.repo.Path, file.Path, file.Staged != "", file.Unstaged == "untracked")
		}
	case diffLoadedMsg:
		if msg.err == nil {
			m.currentDiff = msg.diff
		}
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

	// Show branch menu overlay
	if m.showingBranchMenu {
		return m.renderBranchMenuOverlay(result)
	}

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

func (m model) renderBranchMenuOverlay(background string) string {
	menuStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("170")).
		Background(lipgloss.Color("235")).
		Padding(1).
		Width(60).
		Height(min(len(m.branches)+8, m.height-4))

	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("170")).
		Align(lipgloss.Center)

	itemStyle := lipgloss.NewStyle().
		PaddingLeft(2)

	selectedStyle := lipgloss.NewStyle().
		PaddingLeft(1).
		Background(lipgloss.Color("238")).
		Foreground(lipgloss.Color("170")).
		Bold(true)

	currentStyle := lipgloss.NewStyle().
		PaddingLeft(2).
		Foreground(lipgloss.Color("214"))

	title := titleStyle.Render("Branch Operations")
	content := []string{title, ""}

	// Add "Create new branch" option
	createStyle := itemStyle
	if m.selectedBranchMenu == 0 {
		createStyle = selectedStyle
	}
	content = append(content, createStyle.Render("✨ Create new branch"))
	content = append(content, "")

	// Add existing branches
	for i, branch := range m.branches {
		menuIndex := i + 1
		style := itemStyle
		if m.selectedBranchMenu == menuIndex {
			style = selectedStyle
		}

		prefix := "  "
		branchName := branch.Name
		if branch.IsCurrent {
			style = currentStyle
			prefix = "● "
			branchName += " (current)"
		}

		// Add ahead/behind indicators
		if branch.IsCurrent && (branch.Ahead > 0 || branch.Behind > 0) {
			indicators := ""
			if branch.Ahead > 0 {
				indicators += fmt.Sprintf(" ↑%d", branch.Ahead)
			}
			if branch.Behind > 0 {
				indicators += fmt.Sprintf(" ↓%d", branch.Behind)
			}
			branchName += indicators
		}

		content = append(content, style.Render(prefix+branchName))
	}

	content = append(content, "", "↑↓/jk: navigate • Enter: select • Esc: cancel")

	menu := menuStyle.Render(strings.Join(content, "\n"))

	// Center the menu
	menuTop := (m.height - lipgloss.Height(menu)) / 2
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Top,
		background+strings.Repeat("\n", menuTop)+menu)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
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
		branchName := m.repo.CurrentBranch

		// Add ahead/behind indicators if available
		for _, branch := range m.branches {
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
					branchName += " " + indicators
				}
				break
			}
		}

		repo = fmt.Sprintf("📁 %s  🌿 %s", m.repo.Name, branchName)
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
					statusChar = "A"
					statusStyle = untrackedStyle
				}
			}

			status := statusStyle.Render(statusChar)
			fileName := file.Path

			// Handle renames - show "old -> new"
			if file.OldPath != "" {
				fileName = fmt.Sprintf("%s -> %s", file.OldPath, file.Path)
			}

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

	addStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("42"))

	removeStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("196"))

	lineNumStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("242"))

	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("214"))

	if m.status == nil || len(m.status.Files) == 0 || m.selectedFile >= len(m.status.Files) {
		title := titleStyle.Render("Diff")
		content := title + "\n\n" + "  No file selected"
		return panelStyle.Render(content)
	}

	file := m.status.Files[m.selectedFile]

	// Show filename in title
	title := titleStyle.Render("Diff: " + file.Path)

	// Header info
	content := []string{title, ""}

	// If we have diff content, show it
	if m.currentDiff != "" {
		// Check if this is a binary file (our loadDiff function returns this format)
		if strings.HasPrefix(m.currentDiff, "Binary file ") {
			content = append(content, "", "  📄 Binary file", "", "  This appears to be a binary file and cannot be displayed as text.")
		} else {
			diffLines := strings.Split(m.currentDiff, "\n")

			// Calculate visible lines (leave more space for content)
			availableLines := height - 3 // Account for title and border
			startLine := m.diffScrollOffset
			endLine := startLine + availableLines

			if endLine > len(diffLines) {
				endLine = len(diffLines)
			}

			// Add scroll indicator if needed
			if len(diffLines) > availableLines {
				scrollInfo := fmt.Sprintf(" [%d-%d/%d lines]", startLine+1, min(endLine, len(diffLines)), len(diffLines))
				content[0] = title + lineNumStyle.Render(scrollInfo)
			}

			for i := startLine; i < endLine; i++ {
				if i >= len(diffLines) {
					break
				}

				line := diffLines[i]

				// Style the line based on its prefix
				var styledLine string
				maxWidth := width - 4 // Account for border

				switch {
				case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
					if len(line) > maxWidth {
						line = line[:maxWidth-3] + "..."
					}
					styledLine = headerStyle.Render(line)
				case strings.HasPrefix(line, "@@"):
					if len(line) > maxWidth {
						line = line[:maxWidth-3] + "..."
					}
					styledLine = lineNumStyle.Render(line)
				case strings.HasPrefix(line, "+"):
					if len(line) > maxWidth {
						line = line[:maxWidth-3] + "..."
					}
					// Show whitespace changes more clearly
					lineContent := line[1:] // Remove the + prefix
					if len(strings.TrimSpace(lineContent)) == 0 && len(lineContent) > 0 {
						// Show what kind of whitespace
						whitespaceDesc := ""
						if strings.Contains(lineContent, "\t") {
							whitespaceDesc += "tabs "
						}
						if strings.Contains(lineContent, " ") {
							whitespaceDesc += "spaces "
						}
						if strings.Contains(lineContent, "\r") {
							whitespaceDesc += "CR "
						}
						if whitespaceDesc == "" {
							whitespaceDesc = fmt.Sprintf("%d chars ", len(lineContent))
						}
						styledLine = addStyle.Render(fmt.Sprintf("+ (%s)", strings.TrimSpace(whitespaceDesc)))
					} else {
						styledLine = addStyle.Render(line)
					}
				case strings.HasPrefix(line, "-"):
					if len(line) > maxWidth {
						line = line[:maxWidth-3] + "..."
					}
					// Show whitespace changes more clearly
					lineContent := line[1:] // Remove the - prefix
					if len(strings.TrimSpace(lineContent)) == 0 && len(lineContent) > 0 {
						// Show what kind of whitespace
						whitespaceDesc := ""
						if strings.Contains(lineContent, "\t") {
							whitespaceDesc += "tabs "
						}
						if strings.Contains(lineContent, " ") {
							whitespaceDesc += "spaces "
						}
						if strings.Contains(lineContent, "\r") {
							whitespaceDesc += "CR "
						}
						if whitespaceDesc == "" {
							whitespaceDesc = fmt.Sprintf("%d chars ", len(lineContent))
						}
						styledLine = removeStyle.Render(fmt.Sprintf("- (%s)", strings.TrimSpace(whitespaceDesc)))
					} else {
						styledLine = removeStyle.Render(line)
					}
				default:
					if len(line) > maxWidth {
						line = line[:maxWidth-3] + "..."
					}
					styledLine = line
				}

				content = append(content, styledLine)
			}
		}
	} else if file.Unstaged == "untracked" {
		content = append(content, "", "  Loading file contents...")
	} else {
		// Show if we're waiting for diff or if there's no diff
		status := "staged"
		if file.Staged == "" {
			status = "modified"
		}
		content = append(content, "", fmt.Sprintf("  No diff available for %s file", status))
		content = append(content, "", "  (File may have no changes or loading failed)")
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
			"b: branches • f: fetch • p: pull • P: push • r: refresh • q: quit",
		}
	} else {
		helpLines = []string{
			"tab: switch panel • ↑↓/jk: navigate • space/enter: stage/checkout",
			"h: history mode • s: files mode • b: branches • f: fetch • p: pull • P: push • r: refresh • q: quit",
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