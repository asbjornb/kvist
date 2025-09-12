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
	branchWidth := m.width / 4
	commitWidth := m.width / 2
	filesWidth := m.width - branchWidth - commitWidth

	branches := m.renderBranches(branchWidth, height)
	commits := m.renderCommits(commitWidth, height)
	files := m.renderFiles(filesWidth, height)

	return lipgloss.JoinHorizontal(lipgloss.Top, branches, commits, files)
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
		line := style.Width(width - 2).Render(prefix + branch.Name)
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

func (m model) renderHelp() string {
	helpStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("241")).
		MarginLeft(2)

	return helpStyle.Render("tab: switch panel • ↑↓/jk: navigate • q: quit")
}

func main() {
	p := tea.NewProgram(initialModel(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error running program: %v", err)
		os.Exit(1)
	}
}