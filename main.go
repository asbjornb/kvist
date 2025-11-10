package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/asbjornb/kvist/git"
	"github.com/asbjornb/kvist/workspace"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type panel int

const (
	topPanel    panel = iota // commits, files, or workspaces (left panel in history mode)
	middlePanel              // commit details (top-right panel in history mode only)
	bottomPanel              // diff (bottom-right panel in history mode, bottom panel in files mode)
)

type viewMode int

const (
	workspaceMode       viewMode = iota // showing workspaces + repos
	workspaceManageMode                 // managing workspaces (add/edit/delete)
	historyMode                         // showing commits + details
	filesMode                           // showing files + diff
)

const autoScanInterval = 5 * time.Minute

type modalType int

const (
	workspacePickerModal modalType = iota // workspace selection modal
)

type model struct {
	width          int
	height         int
	ready          bool
	repo           *git.Repository
	commits        []git.Commit
	branches       []git.Branch
	status         *git.Status
	remotes        []git.Remote
	stashes        []git.Stash
	activePanel    panel
	currentMode    viewMode
	selectedCommit int
	selectedBranch int
	selectedFile   int
	err            error
	// Branch operations state
	showingBranchMenu  bool
	creatingBranch     bool
	branchInput        string
	selectedBranchMenu int
	// Diff view state
	currentDiff      string
	diffScrollOffset int
	// Workspace state
	workspaceConfig *workspace.Config
	repoCache       *workspace.RepoCache
	scanner         *workspace.Scanner
	repos           []workspace.RepoInfo
	selectedRepo    int
	scanning        bool
	lastScanTime    time.Time
	loadingRepo     bool // true while loading repository basics
	loadingMetadata bool // true while loading commits/branches/etc

	// Workspace management state
	selectedWorkspace   int
	editingWorkspace    bool
	editingWorkspaceIdx int    // index of workspace being edited, -1 for new workspace
	newWorkspaceName    string
	newWorkspacePath    string
	editingField        int                  // 0 = name, 1 = path
	currentWorkspace    *workspace.Workspace // currently selected workspace
	searchMode        bool                 // whether we're in search mode
	filterText        string               // filter text for repo search
	filteredRepos     []workspace.RepoInfo // filtered list of repos
	scrollOffset      int                  // scroll offset for repo list
	incrementalScanCh <-chan workspace.RepoInfo
	incrementalCancel context.CancelFunc

	// Directory autocomplete state
	dirSuggestions      []string // directory suggestions for path autocomplete
	selectedSuggestion  int      // which suggestion is highlighted

	// Modal state
	showingModal bool      // whether modal is displayed
	modalMode    modalType // what type of modal to show
}

func initialModel() model {
	return model{
		activePanel: topPanel,
		currentMode: workspaceMode,
	}
}

func (m model) Init() tea.Cmd {
	return loadWorkspaceConfig
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

// Incremental loading messages
type repoBasicsLoadedMsg struct {
	repo   *git.Repository
	status *git.Status
	err    error
}

type repoMetadataLoadedMsg struct {
	commits  []git.Commit
	branches []git.Branch
	remotes  []git.Remote
	stashes  []git.Stash
	err      error
}

type autoScanMsg struct{}

type incrementalScanInitMsg struct {
	channel <-chan workspace.RepoInfo
	cancel  context.CancelFunc
}

// Fast loading: repository basics and status for immediate file view
func loadRepositoryBasics(path string) tea.Cmd {
	return func() tea.Msg {
		repo, err := git.OpenRepository(path)
		if err != nil {
			return repoBasicsLoadedMsg{err: err}
		}

		status, err := git.GetStatus(repo.Path)
		if err != nil {
			return repoBasicsLoadedMsg{err: err}
		}

		return repoBasicsLoadedMsg{
			repo:   repo,
			status: status,
		}
	}
}

// Slow loading: commits, branches, remotes, stashes for history view
func loadRepositoryMetadata(path string) tea.Cmd {
	return func() tea.Msg {
		commits, _ := git.GetCommits(path, 50)
		branches, _ := git.GetBranches(path)
		remotes, _ := git.GetRemotes(path)
		stashes, _ := git.GetStashes(path)

		return repoMetadataLoadedMsg{
			commits:  commits,
			branches: branches,
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

type workspaceConfigMsg struct {
	config *workspace.Config
	cache  *workspace.RepoCache
	err    error
}

type workspaceScanMsg struct {
	repos []workspace.RepoInfo
	err   error
}

type repoDiscoveredMsg struct {
	repo workspace.RepoInfo
	err  error
}

type repoCacheUpdatedMsg struct {
	repo workspace.RepoInfo
	err  error
}

type tickMsg time.Time

type autoRefreshMsg time.Time

const autoRefreshInterval = 5 * time.Second

func autoRefreshCmd() tea.Cmd {
	return tea.Tick(autoRefreshInterval, func(t time.Time) tea.Msg {
		return autoRefreshMsg(t)
	})
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

func loadCommitDiff(repoPath string, commitHash string) tea.Cmd {
	return func() tea.Msg {
		diff, err := git.GetCommitDiff(repoPath, commitHash)
		if err != nil {
			// Include git's output in the error message for debugging
			errMsg := fmt.Sprintf("Commit: %s\nRepo: %s\nError: %v\nGit output: %s",
				commitHash, repoPath, err, diff)
			return diffLoadedMsg{diff: "", err: fmt.Errorf("%s", errMsg)}
		}
		return diffLoadedMsg{diff: diff, err: nil}
	}
}

func loadWorkspaceConfig() tea.Msg {
	config, err := workspace.LoadConfig()
	if err != nil {
		return workspaceConfigMsg{err: err}
	}

	cache, err := workspace.LoadRepoCache()
	if err != nil {
		return workspaceConfigMsg{err: err}
	}

	return workspaceConfigMsg{config: config, cache: cache}
}

func scanWorkspaces(scanner *workspace.Scanner) tea.Cmd {
	return func() tea.Msg {
		if scanner == nil {
			return workspaceScanMsg{err: fmt.Errorf("workspace scanner not available")}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		results := scanner.ScanWorkspaces(ctx)
		result := <-results

		return workspaceScanMsg{repos: result.Repos, err: result.Error}
	}
}

func scanSingleWorkspaceIncremental(scanner *workspace.Scanner, ws *workspace.Workspace) tea.Cmd {
	if scanner == nil || ws == nil {
		return func() tea.Msg {
			return workspaceScanMsg{err: fmt.Errorf("workspace scanner not available")}
		}
	}

	workspaceCopy := *ws

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		repoChannel := scanner.DiscoverReposIncremental(ctx, workspaceCopy)
		return incrementalScanInitMsg{channel: repoChannel, cancel: cancel}
	}
}

func incrementalScanNextCmd(scanner *workspace.Scanner, ch <-chan workspace.RepoInfo, cancel context.CancelFunc) tea.Cmd {
	return func() tea.Msg {
		if ch == nil {
			if cancel != nil {
				cancel()
			}
			return workspaceScanMsg{err: fmt.Errorf("no incremental scan channel")}
		}

		repo, ok := <-ch
		if !ok {
			if cancel != nil {
				cancel()
			}
			var (
				repos   []workspace.RepoInfo
				saveErr error
			)
			if scanner != nil {
				saveErr = scanner.SaveCache()
				repos = scanner.GetCachedRepos()
			}
			return workspaceScanMsg{repos: repos, err: saveErr}
		}

		if scanner != nil {
			scanner.UpdateCacheRepo(repo)
		}

		return repoDiscoveredMsg{repo: repo}
	}
}

func refreshRepoMetadata(scanner *workspace.Scanner, repoPath string) tea.Cmd {
	return func() tea.Msg {
		if scanner == nil || repoPath == "" {
			return repoCacheUpdatedMsg{err: fmt.Errorf("workspace scanner not available")}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := scanner.UpdateRepo(ctx, repoPath); err != nil {
			return repoCacheUpdatedMsg{err: err}
		}

		repo, exists := scanner.GetRepo(repoPath)
		if !exists {
			return repoCacheUpdatedMsg{err: fmt.Errorf("repository not found in cache")}
		}

		return repoCacheUpdatedMsg{repo: repo}
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Millisecond*100, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// Load repository incrementally: fast basics first, then metadata
func loadRepositoryIncremental(path string) tea.Cmd {
	return tea.Batch(
		loadRepositoryBasics(path),
		loadRepositoryMetadata(path),
	)
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

		// Handle workspace editing input
		if m.editingWorkspace {
			switch msg.String() {
			case "ctrl+c", "esc":
				m.editingWorkspace = false
				m.newWorkspaceName = ""
				m.newWorkspacePath = ""
				m.editingField = 0
				m.dirSuggestions = nil
				m.selectedSuggestion = 0
			case "tab":
				// If in path field and have suggestions, autocomplete
				if m.editingField == 1 && len(m.dirSuggestions) > 0 && m.selectedSuggestion < len(m.dirSuggestions) {
					m.newWorkspacePath = m.dirSuggestions[m.selectedSuggestion]
					// Add trailing slash if not present to allow continuing
					if m.newWorkspacePath[len(m.newWorkspacePath)-1] != '/' {
						m.newWorkspacePath += "/"
					}
					m.updateDirSuggestions()
				} else {
					// Switch between name and path fields
					m.editingField = (m.editingField + 1) % 2
					if m.editingField == 1 {
						m.updateDirSuggestions()
					}
				}
			case "up":
				// Navigate suggestions if in path field
				if m.editingField == 1 && len(m.dirSuggestions) > 0 {
					if m.selectedSuggestion > 0 {
						m.selectedSuggestion--
					}
				}
			case "down":
				// Navigate suggestions if in path field
				if m.editingField == 1 && len(m.dirSuggestions) > 0 {
					if m.selectedSuggestion < len(m.dirSuggestions)-1 {
						m.selectedSuggestion++
					}
				}
			case "enter":
				if m.newWorkspaceName != "" && m.newWorkspacePath != "" && m.workspaceConfig != nil {
					// Add new workspace
					if err := m.workspaceConfig.AddWorkspace(m.newWorkspaceName, m.newWorkspacePath); err == nil {
						m.editingWorkspace = false
						m.newWorkspaceName = ""
						m.newWorkspacePath = ""
						m.editingField = 0
						m.dirSuggestions = nil
						m.selectedSuggestion = 0
						// Refresh repos if we have a scanner
						if m.scanner != nil {
							m.repos = m.scanner.GetCachedRepos()
						}
					} else {
						m.err = err
					}
				}
			case "backspace":
				// Remove from the active field
				if m.editingField == 0 && len(m.newWorkspaceName) > 0 {
					m.newWorkspaceName = m.newWorkspaceName[:len(m.newWorkspaceName)-1]
				} else if m.editingField == 1 && len(m.newWorkspacePath) > 0 {
					m.newWorkspacePath = m.newWorkspacePath[:len(m.newWorkspacePath)-1]
					m.updateDirSuggestions()
				}
			default:
				// Add printable characters to the active field
				if len(msg.String()) == 1 && msg.String()[0] >= 32 && msg.String()[0] <= 126 {
					if m.editingField == 0 {
						m.newWorkspaceName += msg.String()
					} else {
						m.newWorkspacePath += msg.String()
						m.updateDirSuggestions()
					}
				}
			}
			return m, nil
		}

		// Handle search mode in workspace mode
		if m.currentMode == workspaceMode && m.searchMode && !m.editingWorkspace {
			switch msg.String() {
			case "enter":
				// Exit search mode
				m.searchMode = false
				return m, nil
			case "backspace":
				if len(m.filterText) > 0 {
					m.filterText = m.filterText[:len(m.filterText)-1]
					m.updateFilteredRepos()
				}
				return m, nil
			case "ctrl+c", "esc":
				// Exit search mode and clear filter
				m.searchMode = false
				m.filterText = ""
				m.updateFilteredRepos()
				return m, nil
			default:
				// Add printable characters to filter
				if len(msg.String()) == 1 && msg.String()[0] >= 32 && msg.String()[0] <= 126 {
					m.filterText += msg.String()
					m.updateFilteredRepos()
					return m, nil
				}
			}
		}

		// Handle modal input first
		if m.showingModal {
			switch msg.String() {
			case "ctrl+c", "esc", "q":
				// Close modal
				m.showingModal = false
				return m, nil
			case "up", "k":
				if m.modalMode == workspacePickerModal {
					if m.editingWorkspace && m.editingField == 1 && len(m.dirSuggestions) > 0 {
						// Navigate suggestions
						if m.selectedSuggestion > 0 {
							m.selectedSuggestion--
						}
					} else if !m.editingWorkspace && m.selectedWorkspace > 0 {
						m.selectedWorkspace--
					}
				}
			case "down", "j":
				if m.modalMode == workspacePickerModal {
					if m.editingWorkspace && m.editingField == 1 && len(m.dirSuggestions) > 0 {
						// Navigate suggestions
						if m.selectedSuggestion < len(m.dirSuggestions)-1 {
							m.selectedSuggestion++
						}
					} else if !m.editingWorkspace {
						maxWorkspaces := len(m.workspaceConfig.Workspaces)
						maxWorkspaces++ // Add 1 for "Add New Workspace" option
						if m.selectedWorkspace < maxWorkspaces-1 {
							m.selectedWorkspace++
						}
					}
				}
			case " ", "enter":
				if m.modalMode == workspacePickerModal && m.workspaceConfig != nil {
					if m.editingWorkspace {
						if m.newWorkspaceName != "" && m.newWorkspacePath != "" {
							if m.editingWorkspaceIdx >= 0 {
								// Update existing workspace
								expandedPath := workspace.ExpandPath(m.newWorkspacePath)

								// Verify path exists and is a directory
								if stat, err := os.Stat(expandedPath); err != nil {
									m.err = fmt.Errorf("path does not exist: %w", err)
								} else if !stat.IsDir() {
									m.err = fmt.Errorf("path is not a directory: %s", expandedPath)
								} else {
									m.workspaceConfig.Workspaces[m.editingWorkspaceIdx].Name = m.newWorkspaceName
									m.workspaceConfig.Workspaces[m.editingWorkspaceIdx].Path = expandedPath
									if err := m.workspaceConfig.Save(); err == nil {
										m.editingWorkspace = false
										m.editingWorkspaceIdx = -1
										m.newWorkspaceName = ""
										m.newWorkspacePath = ""
										m.dirSuggestions = nil
										m.selectedSuggestion = 0
										// Refresh repos if we have a scanner
										if m.scanner != nil {
											m.repos = m.scanner.GetCachedRepos()
											m.updateFilteredRepos()
										}
									} else {
										m.err = err
									}
								}
							} else {
								// Create new workspace
								err := m.workspaceConfig.AddWorkspace(m.newWorkspaceName, m.newWorkspacePath)
								if err != nil {
									m.err = err
								} else {
									// Find the newly added workspace and select it
									for i, ws := range m.workspaceConfig.Workspaces {
										if ws.Name == m.newWorkspaceName {
											m.currentWorkspace = &m.workspaceConfig.Workspaces[i]
											break
										}
									}

									// Track this as the last accessed workspace
									if m.scanner != nil {
										m.scanner.UpdateLastWorkspace(m.currentWorkspace.Name)
										// Save state to disk (best effort, don't block on errors)
										go func() {
											if m.scanner != nil {
												_ = m.scanner.SaveCache()
											}
										}()
									}

									// Close modal and load repos
									m.showingModal = false
									m.editingWorkspace = false
									m.editingWorkspaceIdx = -1

									if m.scanner != nil {
										m.repos = m.scanner.GetCachedRepos()
										m.updateFilteredRepos()
									}
								}
							}
						}
					} else if m.selectedWorkspace == len(m.workspaceConfig.Workspaces) {
						// "Add New Workspace" selected
						m.editingWorkspace = true
						m.editingWorkspaceIdx = -1
						m.newWorkspaceName = ""
						m.newWorkspacePath = "~/"
						m.editingField = 0 // Start with name field
						m.updateDirSuggestions()
						return m, tickCmd() // Continue ticking for cursor animation
					} else if m.selectedWorkspace < len(m.workspaceConfig.Workspaces) {
						// Open workspace - show repos from this workspace only
						m.currentWorkspace = &m.workspaceConfig.Workspaces[m.selectedWorkspace]
						m.showingModal = false // Close modal

						// Track this as the last accessed workspace
						if m.scanner != nil {
							m.scanner.UpdateLastWorkspace(m.currentWorkspace.Name)
							// Save state to disk (best effort, don't block on errors)
							go func() {
								if cache := m.scanner.GetCache(); cache != nil {
									cache.Save()
								}
							}()
						}

						// Load all cached repos and let updateFilteredRepos() handle workspace filtering
						if m.scanner != nil {
							m.repos = m.scanner.GetCachedRepos()
							m.updateFilteredRepos()
						}
					}
				}
			case "tab":
				// Switch between name and path fields when editing
				if m.modalMode == workspacePickerModal && m.editingWorkspace {
					// If in path field and have suggestions, autocomplete
					if m.editingField == 1 && len(m.dirSuggestions) > 0 && m.selectedSuggestion < len(m.dirSuggestions) {
						m.newWorkspacePath = m.dirSuggestions[m.selectedSuggestion]
						// Add trailing slash if not present to allow continuing
						if m.newWorkspacePath[len(m.newWorkspacePath)-1] != '/' {
							m.newWorkspacePath += "/"
						}
						m.updateDirSuggestions()
					} else {
						// Switch between name and path fields
						m.editingField = (m.editingField + 1) % 2
						if m.editingField == 1 {
							m.updateDirSuggestions()
						}
					}
				}
			case "d":
				// Delete workspace from modal
				if m.modalMode == workspacePickerModal && !m.editingWorkspace && m.workspaceConfig != nil {
					if m.selectedWorkspace < len(m.workspaceConfig.Workspaces) {
						// Delete the selected workspace
						workspaces := m.workspaceConfig.Workspaces
						m.workspaceConfig.Workspaces = append(workspaces[:m.selectedWorkspace], workspaces[m.selectedWorkspace+1:]...)
						if err := m.workspaceConfig.Save(); err == nil {
							// Adjust selection if needed
							if m.selectedWorkspace >= len(m.workspaceConfig.Workspaces) && m.selectedWorkspace > 0 {
								m.selectedWorkspace--
							}
							// Refresh repos if we have a scanner
							if m.scanner != nil {
								m.repos = m.scanner.GetCachedRepos()
								m.updateFilteredRepos()
							}
						} else {
							m.err = err
						}
					}
				}
			case "e":
				// Edit workspace from modal
				if m.modalMode == workspacePickerModal && !m.editingWorkspace && m.workspaceConfig != nil {
					if m.selectedWorkspace < len(m.workspaceConfig.Workspaces) {
						// Enter edit mode for selected workspace
						ws := m.workspaceConfig.Workspaces[m.selectedWorkspace]
						m.editingWorkspace = true
						m.editingWorkspaceIdx = m.selectedWorkspace
						m.newWorkspaceName = ws.Name
						m.newWorkspacePath = ws.Path
						m.editingField = 0
						m.updateDirSuggestions()
						return m, tickCmd()
					}
				}
			case "backspace":
				if m.modalMode == workspacePickerModal && m.editingWorkspace {
					if m.editingField == 0 && len(m.newWorkspaceName) > 0 {
						m.newWorkspaceName = m.newWorkspaceName[:len(m.newWorkspaceName)-1]
					} else if m.editingField == 1 && len(m.newWorkspacePath) > 0 {
						m.newWorkspacePath = m.newWorkspacePath[:len(m.newWorkspacePath)-1]
						m.updateDirSuggestions()
					}
				}
			default:
				// Handle text input for workspace editing
				if m.modalMode == workspacePickerModal && m.editingWorkspace {
					if len(msg.String()) == 1 && msg.String()[0] >= 32 && msg.String()[0] <= 126 {
						if m.editingField == 0 {
							m.newWorkspaceName += msg.String()
						} else {
							m.newWorkspacePath += msg.String()
							m.updateDirSuggestions()
						}
					}
				}
			}
			return m, nil // Modal consumes all input
		}

		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "tab":
			if m.currentMode == historyMode {
				// In history mode, cycle through 3 panels: top -> middle -> bottom -> top
				m.activePanel = (m.activePanel + 1) % 3
			} else {
				// In other modes, cycle through 2 panels: top -> bottom -> top
				if m.activePanel == topPanel {
					m.activePanel = bottomPanel
				} else {
					m.activePanel = topPanel
				}
			}
		case "shift+tab":
			if m.currentMode == historyMode {
				// In history mode, cycle backwards through 3 panels: top <- middle <- bottom <- top
				m.activePanel = (m.activePanel + 2) % 3
			} else {
				// In other modes, cycle through 2 panels (same as tab since only 2 panels)
				if m.activePanel == topPanel {
					m.activePanel = bottomPanel
				} else {
					m.activePanel = topPanel
				}
			}
		case "up", "k":
			switch m.activePanel {
			case topPanel:
				if m.currentMode == workspaceMode {
					if m.selectedRepo > 0 {
						m.selectedRepo--
					}
				} else if m.currentMode == workspaceManageMode {
					if m.selectedWorkspace > 0 {
						m.selectedWorkspace--
					}
				} else if m.currentMode == historyMode {
					if m.selectedCommit > 0 {
						m.selectedCommit--
						m.diffScrollOffset = 0
						if m.repo != nil && m.selectedCommit < len(m.commits) {
							commit := m.commits[m.selectedCommit]
							return m, loadCommitDiff(m.repo.Path, commit.Hash)
						}
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
			case middlePanel:
				// Middle panel in history mode - could add scrolling for long commit messages later
				// For now, no scrolling needed
			case bottomPanel:
				if (m.currentMode == filesMode || m.currentMode == historyMode) && m.diffScrollOffset > 0 {
					m.diffScrollOffset--
				}
			}
		case "down", "j":
			switch m.activePanel {
			case topPanel:
				if m.currentMode == workspaceMode {
					if len(m.filteredRepos) == 0 {
						m.updateFilteredRepos() // Ensure filtered list is initialized
					}
					if m.selectedRepo < len(m.filteredRepos)-1 {
						m.selectedRepo++
					}
				} else if m.currentMode == workspaceManageMode {
					maxItems := len(m.workspaceConfig.Workspaces) + 1 // +1 for "Add New Workspace"
					if m.selectedWorkspace < maxItems-1 {
						m.selectedWorkspace++
					}
				} else if m.currentMode == historyMode {
					if m.selectedCommit < len(m.commits)-1 {
						m.selectedCommit++
						m.diffScrollOffset = 0
						if m.repo != nil && m.selectedCommit < len(m.commits) {
							commit := m.commits[m.selectedCommit]
							return m, loadCommitDiff(m.repo.Path, commit.Hash)
						}
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
			case middlePanel:
				// Middle panel in history mode - could add scrolling for long commit messages later
				// For now, no scrolling needed
			case bottomPanel:
				if (m.currentMode == filesMode || m.currentMode == historyMode) && m.currentDiff != "" {
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
			if m.currentMode == workspaceMode {
				if cmd := m.startWorkspaceScan(); cmd != nil {
					return m, cmd
				}
			} else if m.currentMode != filesMode {
				// Refresh current repository with incremental loading
				// Note: filesMode has auto-refresh, manual refresh not needed
				m.loadingRepo = true
				m.loadingMetadata = true
				return m, loadRepositoryIncremental(".")
			}
		case "w":
			if m.currentMode == workspaceMode {
				// Show workspace picker modal
				m.showingModal = true
				m.modalMode = workspacePickerModal
				m.selectedWorkspace = 0
				m.editingWorkspace = false
				m.newWorkspaceName = ""
				m.newWorkspacePath = ""
				m.editingField = 0
				return m, tickCmd() // Start ticking for cursor animation
			} else {
				// Go to workspace mode
				m.currentMode = workspaceMode
				m.currentDiff = ""
			}
		case "h":
			m.currentMode = historyMode
			m.diffScrollOffset = 0
			// Load diff for currently selected commit
			if m.repo != nil && len(m.commits) > 0 && m.selectedCommit < len(m.commits) {
				commit := m.commits[m.selectedCommit]
				return m, loadCommitDiff(m.repo.Path, commit.Hash)
			}
			m.currentDiff = ""
		case "s":
			m.currentMode = filesMode
			m.diffScrollOffset = 0 // Reset scroll when switching to files mode
		case "/":
			if m.currentMode == workspaceMode {
				// Enter search mode
				m.searchMode = true
				m.filterText = ""
				m.updateFilteredRepos()
				return m, tickCmd() // Start cursor animation
			}
		case "d":
			if m.currentMode == workspaceManageMode && m.workspaceConfig != nil && m.selectedWorkspace < len(m.workspaceConfig.Workspaces) {
				// Delete selected workspace
				workspaces := m.workspaceConfig.Workspaces
				m.workspaceConfig.Workspaces = append(workspaces[:m.selectedWorkspace], workspaces[m.selectedWorkspace+1:]...)
				if err := m.workspaceConfig.Save(); err == nil {
					// Adjust selection if needed
					if m.selectedWorkspace >= len(m.workspaceConfig.Workspaces) && m.selectedWorkspace > 0 {
						m.selectedWorkspace--
					}
					// Refresh repos if we have a scanner
					if m.scanner != nil {
						m.repos = m.scanner.GetCachedRepos()
					}
				}
			}
		case "b":
			if m.repo != nil {
				m.showingBranchMenu = true
				m.selectedBranchMenu = 0
			}
		case " ", "enter":
			if m.currentMode == workspaceMode && len(m.filteredRepos) > 0 && m.selectedRepo < len(m.filteredRepos) {
				// Switch to selected repository with incremental loading
				selectedRepo := m.filteredRepos[m.selectedRepo]
				m.currentMode = filesMode
				m.selectedFile = 0
				m.diffScrollOffset = 0
				m.loadingRepo = true
				m.loadingMetadata = true

				// Track this as the last accessed repository
				if m.scanner != nil {
					m.scanner.UpdateLastRepo(selectedRepo.Path)
					// Save state to disk (best effort, don't block on errors)
					go func() {
						if m.scanner != nil {
							_ = m.scanner.SaveCache()
						}
					}()
				}

				return m, loadRepositoryIncremental(selectedRepo.Path)
			} else if m.currentMode == workspaceManageMode && m.workspaceConfig != nil {
				if m.selectedWorkspace == len(m.workspaceConfig.Workspaces) {
					// "Add New Workspace" selected
					m.editingWorkspace = true
					m.newWorkspaceName = ""
					m.newWorkspacePath = "~/"
					m.editingField = 0 // Start with name field
					m.updateDirSuggestions()
					return m, tickCmd() // Start tick for cursor animation
				} else if m.selectedWorkspace < len(m.workspaceConfig.Workspaces) {
					// Open workspace - show repos from this workspace only
					m.currentWorkspace = &m.workspaceConfig.Workspaces[m.selectedWorkspace]
					m.currentMode = workspaceMode

					// Track this as the last accessed workspace
					if m.scanner != nil {
						m.scanner.UpdateLastWorkspace(m.currentWorkspace.Name)
						// Save state to disk (best effort, don't block on errors)
						go func() {
							if m.scanner != nil {
								_ = m.scanner.SaveCache()
							}
						}()
					}

					// Load all cached repos and let updateFilteredRepos() handle workspace filtering
					if m.scanner != nil {
						m.repos = m.scanner.GetCachedRepos()
						m.updateFilteredRepos()
					}

					if cmd := m.startWorkspaceScan(); cmd != nil {
						return m, cmd
					}
				}
			} else if m.activePanel == topPanel && m.currentMode == filesMode && m.status != nil && m.selectedFile < len(m.status.Files) {
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
	case repoBasicsLoadedMsg:
		// Fast loading: repository and status loaded - can show files immediately
		m.loadingRepo = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}

		m.repo = msg.repo
		m.status = msg.status

		// Load diff for currently selected file to preserve user's view during auto-refresh
		if m.currentMode == filesMode && m.repo != nil && m.status != nil && len(m.status.Files) > 0 {
			// Ensure selectedFile is within bounds after status update
			if m.selectedFile >= len(m.status.Files) {
				m.selectedFile = len(m.status.Files) - 1
			}
			// Load diff for the currently selected file, not always file[0]
			file := m.status.Files[m.selectedFile]
			return m, tea.Batch(
				loadDiff(m.repo.Path, file.Path, file.Staged != "", file.Unstaged == "untracked"),
				autoRefreshCmd(), // Start auto-refresh timer
			)
		}
		// Start auto-refresh even if no files to diff
		return m, autoRefreshCmd()
	case repoMetadataLoadedMsg:
		// Slow loading: commits, branches, etc loaded - history view now available
		m.loadingMetadata = false
		if msg.err != nil {
			// Don't overwrite existing error, just log metadata loading failure
			if m.err == nil {
				m.err = fmt.Errorf("failed to load repository metadata: %w", msg.err)
			}
			return m, nil
		}

		m.commits = msg.commits
		m.branches = msg.branches
		m.remotes = msg.remotes
		m.stashes = msg.stashes
	case diffLoadedMsg:
		if msg.err == nil {
			m.currentDiff = msg.diff
		} else {
			// Show error in diff panel
			m.currentDiff = fmt.Sprintf("Error loading diff: %v", msg.err)
		}
	case workspaceConfigMsg:
		if msg.err != nil {
			m.err = msg.err
		} else {
			m.workspaceConfig = msg.config
			m.repoCache = msg.cache
			m.scanner = workspace.NewScanner(msg.config, msg.cache)
			// Load cached repos immediately
			m.repos = m.scanner.GetCachedRepos()

			var cmds []tea.Cmd
			if startupCmd := m.smartStartup(); startupCmd != nil {
				cmds = append(cmds, startupCmd)
			}

			if scanCmd := m.startWorkspaceScan(); scanCmd != nil {
				cmds = append(cmds, scanCmd)
			}

			if m.workspaceConfig != nil && len(m.workspaceConfig.Workspaces) > 0 {
				cmds = append(cmds, scheduleAutoScan())
			}

			switch len(cmds) {
			case 0:
				return m, nil
			case 1:
				return m, cmds[0]
			default:
				return m, tea.Batch(cmds...)
			}
		}
	case incrementalScanInitMsg:
		m.incrementalScanCh = msg.channel
		m.incrementalCancel = msg.cancel
		if m.scanner != nil && m.incrementalScanCh != nil {
			return m, incrementalScanNextCmd(m.scanner, m.incrementalScanCh, m.incrementalCancel)
		}
	case repoDiscoveredMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		if m.scanner != nil {
			m.repos = m.scanner.GetCachedRepos()
			m.updateFilteredRepos()
		}
		if m.incrementalScanCh != nil && m.scanner != nil {
			return m, incrementalScanNextCmd(m.scanner, m.incrementalScanCh, m.incrementalCancel)
		}
	case repoCacheUpdatedMsg:
		if msg.err != nil {
			if m.err == nil {
				m.err = msg.err
			}
			return m, nil
		}
		if m.scanner != nil {
			m.repos = m.scanner.GetCachedRepos()
			m.updateFilteredRepos()
		}
		return m, nil
	case autoScanMsg:
		if m.scanning {
			return m, nil
		}
		if cmd := m.startWorkspaceScan(); cmd != nil {
			return m, cmd
		}
		if m.workspaceConfig != nil && len(m.workspaceConfig.Workspaces) > 0 {
			return m, scheduleAutoScan()
		}
		return m, nil
	case workspaceScanMsg:
		if m.incrementalCancel != nil {
			m.incrementalCancel = nil
		}
		m.incrementalScanCh = nil
		m.scanning = false
		m.lastScanTime = time.Now()
		var cmds []tea.Cmd
		if msg.err == nil {
			m.err = nil
			// Always load the complete cached repo list
			// Let updateFilteredRepos() handle workspace filtering for display
			m.repos = m.scanner.GetCachedRepos()
			m.updateFilteredRepos()
		} else if m.err == nil {
			m.err = msg.err
		}
		if m.workspaceConfig != nil && len(m.workspaceConfig.Workspaces) > 0 {
			cmds = append(cmds, scheduleAutoScan())
		}
		switch len(cmds) {
		case 0:
			return m, nil
		case 1:
			return m, cmds[0]
		default:
			return m, tea.Batch(cmds...)
		}
	case tickMsg:
		// Continue ticking if scanning, editing workspace, in search mode, or showing modal
		if m.scanning || m.editingWorkspace || m.searchMode || m.showingModal {
			return m, tickCmd()
		}
	case autoRefreshMsg:
		// Periodic auto-refresh of git status when viewing a repo
		if m.repo != nil && !m.loadingRepo {
			// Capture repo for closure
			repo := m.repo
			// Reload git status only (faster than full reload)
			// Note: Don't schedule next refresh here - repoBasicsLoadedMsg handler will do it
			return m, func() tea.Msg {
				status, err := git.GetStatus(repo.Path)
				if err != nil {
					return repoBasicsLoadedMsg{err: err}
				}
				return repoBasicsLoadedMsg{repo: repo, status: status}
			}
		}
		// If no repo loaded, don't schedule next refresh
		return m, nil
	case gitOperationMsg:
		if msg.err == nil {
			// Refresh repository with incremental loading
			m.loadingRepo = true
			m.loadingMetadata = true
			repoPath := "."
			if m.repo != nil {
				repoPath = m.repo.Path
			}
			cmds := []tea.Cmd{loadRepositoryIncremental(repoPath)}
			if m.scanner != nil && m.repo != nil {
				cmds = append(cmds, refreshRepoMetadata(m.scanner, m.repo.Path))
			}
			return m, tea.Batch(cmds...)
		}
	case fileOperationMsg:
		if msg.err == nil {
			// Refresh repository with incremental loading
			m.loadingRepo = true
			m.loadingMetadata = true
			repoPath := "."
			if m.repo != nil {
				repoPath = m.repo.Path
			}
			return m, loadRepositoryIncremental(repoPath)
		}
	case branchOperationMsg:
		if msg.err == nil {
			// Refresh repository with incremental loading
			m.loadingRepo = true
			m.loadingMetadata = true
			repoPath := "."
			if m.repo != nil {
				repoPath = m.repo.Path
			}
			return m, loadRepositoryIncremental(repoPath)
		}
	}
	return m, nil
}

// smartStartup determines the best startup mode based on cached session state
func (m *model) smartStartup() tea.Cmd {
	// Check if we have session state
	if m.repoCache.LastRepoPath != "" {
		// Try to restore to the last repository
		if repo, exists := m.repoCache.Repos[m.repoCache.LastRepoPath]; exists {
			// Set the workspace context
			for _, ws := range m.workspaceConfig.Workspaces {
				if ws.Name == repo.WorkspaceName {
					m.currentWorkspace = &ws
					break
				}
			}

			// Load the repository and go to files mode
			m.currentMode = filesMode
			m.selectedFile = 0
			m.diffScrollOffset = 0
			m.loadingRepo = true
			m.loadingMetadata = true

			m.updateFilteredRepos()
			// Return command to load the last repository
			return loadRepositoryIncremental(m.repoCache.LastRepoPath)
		}
	}

	// Fallback: If we have a last workspace, open that workspace
	if m.repoCache.LastWorkspace != "" {
		for _, ws := range m.workspaceConfig.Workspaces {
			if ws.Name == m.repoCache.LastWorkspace {
				m.currentWorkspace = &ws
				m.currentMode = workspaceMode
				m.updateFilteredRepos()
				return nil
			}
		}
	}

	// Final fallback: Show workspace selection if we have workspaces
	if len(m.workspaceConfig.Workspaces) > 0 {
		m.currentMode = workspaceManageMode
		m.selectedWorkspace = 0
		m.editingWorkspace = false
	} else {
		// No workspaces configured, stay in mixed mode
		m.currentMode = workspaceMode
		m.currentWorkspace = nil
		m.updateFilteredRepos()
	}

	return nil
}

func (m *model) startWorkspaceScan() tea.Cmd {
	if m.scanner == nil {
		return nil
	}

	if m.currentWorkspace == nil {
		if m.workspaceConfig == nil || m.repoCache == nil || len(m.workspaceConfig.Workspaces) == 0 {
			return nil
		}
	}

	if m.incrementalCancel != nil {
		m.incrementalCancel()
		m.incrementalCancel = nil
	}
	m.incrementalScanCh = nil

	var scanCmd tea.Cmd
	if m.currentWorkspace != nil {
		scanCmd = scanSingleWorkspaceIncremental(m.scanner, m.currentWorkspace)
	} else {
		scanCmd = scanWorkspaces(m.scanner)
	}
	if scanCmd == nil {
		return nil
	}

	m.scanning = true
	return tea.Batch(scanCmd, tickCmd())
}

func scheduleAutoScan() tea.Cmd {
	return tea.Tick(autoScanInterval, func(time.Time) tea.Msg {
		return autoScanMsg{}
	})
}

func (m model) View() string {
	if !m.ready {
		return "\n  Initializing..."
	}

	if m.err != nil {
		return fmt.Sprintf("\n  Error: %v\n\n  Make sure you're in a git repository.\n", m.err)
	}

	// In workspace modes, we don't need a repo loaded
	if m.currentMode != workspaceMode && m.currentMode != workspaceManageMode {
		if m.repo == nil {
			if m.loadingRepo {
				return "\n  Loading repository..."
			} else {
				return "\n  No repository loaded"
			}
		}

		// For history mode, we need commits loaded
		if m.currentMode == historyMode && m.commits == nil && m.loadingMetadata {
			return "\n  Loading commit history..."
		}
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
				strings.Repeat("\n", overlayTop)+overlay)
	}

	// Show modal overlay
	if m.showingModal {
		return m.renderModalOverlay(result)
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

	// Position menu in center as a proper modal overlay
	menuTop := (m.height - lipgloss.Height(menu)) / 2

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Top, background) +
		lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Top,
			strings.Repeat("\n", menuTop)+menu)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (m model) renderModalOverlay(background string) string {
	modalStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("170")).
		Background(lipgloss.Color("235")).
		Padding(1).
		Width(70).
		Height(min(15, m.height-4))

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

	switch m.modalMode {
	case workspacePickerModal:
		titleText := "📂 Select Workspace"
		if m.editingWorkspace {
			if m.editingWorkspaceIdx >= 0 {
				titleText = "✏️  Edit Workspace"
			} else {
				titleText = "➕ Add Workspace"
			}
		}
		title := titleStyle.Render(titleText)
		content := []string{title, ""}

		if m.editingWorkspace {
			// Show workspace editing/creation form
			nameLabel := "Name:"
			pathLabel := "Path:"

			nameCursor := ""
			pathCursor := ""
			if m.editingField == 0 {
				nameCursor = "█"
			} else {
				pathCursor = "█"
			}

			content = append(content,
				fmt.Sprintf("  %s %s%s", nameLabel, m.newWorkspaceName, nameCursor),
				fmt.Sprintf("  %s %s%s", pathLabel, m.newWorkspacePath, pathCursor),
			)

			// Show directory suggestions if in path field
			if m.editingField == 1 && len(m.dirSuggestions) > 0 {
				content = append(content, "")
				suggestionStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
				selectedSuggestionStyle := lipgloss.NewStyle().
					Foreground(lipgloss.Color("214")).
					Background(lipgloss.Color("238"))

				maxVisible := 5
				totalSuggestions := len(m.dirSuggestions)

				// Calculate scroll window to keep selected item visible
				scrollOffset := 0
				if m.selectedSuggestion >= maxVisible {
					scrollOffset = m.selectedSuggestion - maxVisible + 1
				}
				endIdx := scrollOffset + maxVisible
				if endIdx > totalSuggestions {
					endIdx = totalSuggestions
				}

				// Show range indicator if there are more items
				if totalSuggestions > maxVisible {
					rangeText := fmt.Sprintf("  [%d-%d of %d]", scrollOffset+1, endIdx, totalSuggestions)
					content = append(content, suggestionStyle.Render(rangeText))
				}

				for i := scrollOffset; i < endIdx; i++ {
					suggestion := m.dirSuggestions[i]
					if i == m.selectedSuggestion {
						content = append(content, selectedSuggestionStyle.Render("  ▶ "+suggestion))
					} else {
						content = append(content, suggestionStyle.Render("    "+suggestion))
					}
				}
			}

			helpAction := "create"
			if m.editingWorkspaceIdx >= 0 {
				helpAction = "update"
			}
			content = append(content,
				"",
				fmt.Sprintf("  Tab: autocomplete/next field • ↑↓: navigate • Enter: %s • Esc: cancel", helpAction),
			)
		} else {
			// Show workspace list
			if m.workspaceConfig != nil {
				for i, ws := range m.workspaceConfig.Workspaces {
					text := fmt.Sprintf("📂 %s (%s)", ws.Name, ws.Path)
					if i == m.selectedWorkspace {
						content = append(content, selectedStyle.Render("▶ "+text))
					} else {
						content = append(content, itemStyle.Render("  "+text))
					}
				}

				// Add "New Workspace" option
				addText := "➕ Add New Workspace"
				if m.selectedWorkspace == len(m.workspaceConfig.Workspaces) {
					content = append(content, selectedStyle.Render("▶ "+addText))
				} else {
					content = append(content, itemStyle.Render("  "+addText))
				}
			}

			content = append(content, "", "  Enter: select • e: edit • d: delete • Esc: close")
		}

		modal := modalStyle.Render(strings.Join(content, "\n"))

		// Position modal in center
		overlayHeight := modalStyle.GetHeight()
		overlayTop := (m.height - overlayHeight) / 2

		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Top, background) +
			lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Top,
				strings.Repeat("\n", overlayTop)+modal)
	}

	return background
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
		statusInfo := ""

		// Add ahead/behind indicators if available
		if m.branches != nil {
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
						statusInfo += " [origin: " + indicators + "]"
					}
					break
				}
			}

			// Add ahead/behind vs main if not on main
			if branchName != "main" && branchName != "master" {
				ahead, behind, ok := git.GetAheadBehindBranch(m.repo.Path, "main")
				if !ok {
					// Try master if main doesn't exist
					ahead, behind, ok = git.GetAheadBehindBranch(m.repo.Path, "master")
				}
				if ok && (ahead > 0 || behind > 0) {
					mainIndicators := ""
					if ahead > 0 {
						mainIndicators += fmt.Sprintf("↑%d", ahead)
					}
					if behind > 0 {
						if mainIndicators != "" {
							mainIndicators += " "
						}
						mainIndicators += fmt.Sprintf("↓%d", behind)
					}
					if mainIndicators != "" {
						statusInfo += " [main: " + mainIndicators + "]"
					}
				}
			}
		} else if m.loadingMetadata {
			statusInfo = " (loading...)"
		}

		repo = fmt.Sprintf("📁 %s  🌿 %s%s", m.repo.Name, branchName, statusInfo)
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
	// Two-panel vertical layout with mode-specific splits
	var topHeight, bottomHeight int

	// Files mode: give more space to diff (bottom panel)
	// Other modes: balanced split
	if m.currentMode == filesMode {
		topHeight = height * 2 / 5      // 40% for file list
		bottomHeight = height - topHeight // 60% for diff
	} else {
		topHeight = height * 2 / 3      // 66% for top panel
		bottomHeight = height - topHeight // 33% for bottom panel
	}

	// Content depends on current mode
	if m.currentMode == historyMode {
		// 3-panel layout for history mode: left (commits) | top-right (details) / bottom-right (diff)
		leftWidth := m.width * 40 / 100      // 40% for commit list
		rightWidth := m.width - leftWidth     // 60% for right side
		rightTopHeight := height * 30 / 100   // 30% of total height for commit details
		rightBottomHeight := height - rightTopHeight // 70% for diff

		left := m.renderCommits(leftWidth, height)
		topRight := m.renderCommitDetails(rightWidth, rightTopHeight)
		bottomRight := m.renderCommitDiff(rightWidth, rightBottomHeight)

		// Stack right panels vertically
		rightSide := lipgloss.JoinVertical(lipgloss.Top, topRight, bottomRight)

		// Join left and right horizontally
		return lipgloss.JoinHorizontal(lipgloss.Top, left, rightSide)
	}

	// 2-panel vertical layout for other modes
	var top, bottom string
	if m.currentMode == workspaceMode {
		top = m.renderWorkspaces(m.width, topHeight)
		bottom = m.renderRepoDetails(m.width, bottomHeight)
	} else if m.currentMode == workspaceManageMode {
		top = m.renderWorkspaceManager(m.width, topHeight)
		bottom = m.renderWorkspaceHelp(m.width, bottomHeight)
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
		// Calculate scrolling bounds
		visibleItems := height - 3 // Reserve space for title and margins

		// Calculate scroll window to keep selected file visible
		startIdx := 0
		if m.selectedFile >= visibleItems {
			startIdx = m.selectedFile - visibleItems + 1
		}
		endIdx := startIdx + visibleItems
		if endIdx > len(m.status.Files) {
			endIdx = len(m.status.Files)
		}

		for i := startIdx; i < endIdx; i++ {
			file := m.status.Files[i]

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
			if m.activePanel == middlePanel {
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
				content = append(content, lipgloss.NewStyle().PaddingLeft(2).Render("..."))
				break
			}
			content = append(content, lipgloss.NewStyle().PaddingLeft(2).Width(width-4).Render(line))
		}
	}

	return panelStyle.Render(strings.Join(content, "\n"))
}

func (m model) renderCommitDiff(width, height int) string {
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
		Foreground(lipgloss.Color("42")) // Green

	removeStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("196")) // Red

	lineNumStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("242")) // Gray

	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("214")) // Orange

	diffHeaderStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("226")).Bold(true) // Yellow

	title := titleStyle.Render("Diff")
	content := []string{title, ""}

	if m.currentDiff == "" {
		content = append(content, lineNumStyle.Render("  Loading diff..."))
		return panelStyle.Render(strings.Join(content, "\n"))
	}

	// Render diff with syntax highlighting
	lines := strings.Split(m.currentDiff, "\n")
	maxDiffLines := height - 4 // Leave space for title and padding

	// Apply scroll offset
	startLine := m.diffScrollOffset
	endLine := startLine + maxDiffLines
	if endLine > len(lines) {
		endLine = len(lines)
	}

	if startLine < len(lines) {
		for i := startLine; i < endLine; i++ {
			line := lines[i]
			var styledLine string
			maxWidth := width - 6 // Account for padding and borders

			// Syntax highlighting for diff
			switch {
			case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
				// File headers
				if len(line) > maxWidth {
					line = line[:maxWidth-3] + "..."
				}
				styledLine = headerStyle.Render(line)
			case strings.HasPrefix(line, "@@"):
				// Hunk headers
				if len(line) > maxWidth {
					line = line[:maxWidth-3] + "..."
				}
				styledLine = lineNumStyle.Render(line)
			case strings.HasPrefix(line, "+"):
				// Additions
				if len(line) > maxWidth {
					line = line[:maxWidth-3] + "..."
				}
				styledLine = addStyle.Render(line)
			case strings.HasPrefix(line, "-"):
				// Deletions
				if len(line) > maxWidth {
					line = line[:maxWidth-3] + "..."
				}
				styledLine = removeStyle.Render(line)
			case strings.HasPrefix(line, "diff --git"):
				// Diff headers
				if len(line) > maxWidth {
					line = line[:maxWidth-3] + "..."
				}
				styledLine = diffHeaderStyle.Render(line)
			default:
				if len(line) > maxWidth {
					line = line[:maxWidth-3] + "..."
				}
				styledLine = line
			}

			content = append(content, lipgloss.NewStyle().PaddingLeft(2).Render(styledLine))
		}

		// Show scroll indicator if there's more content
		if endLine < len(lines) || startLine > 0 {
			scrollInfo := fmt.Sprintf("[%d-%d/%d lines]", startLine+1, endLine, len(lines))
			content = append(content, "", lineNumStyle.Render(scrollInfo))
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
			"tab: panels • ↑↓/jk: nav • space: stage • w: workspace/manage • h: history • s: files",
			"b: branches • f: fetch • p: pull • P: push • r: refresh • q: quit",
		}
	} else {
		helpLines = []string{
			"tab: switch panel • ↑↓/jk: navigate • space/enter: stage/checkout",
			"w: workspace/manage • h: history mode • s: files mode • b: branches • f: fetch • p: pull • P: push • r: refresh • q: quit",
		}
	}

	return helpStyle.Render(strings.Join(helpLines, "\n"))
}

func (m model) renderWorkspaces(width, height int) string {
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

	workspaceStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("214")).
		Bold(true)

	repoNameStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("117"))

	branchStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("84"))

	statusStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("203"))

	itemStyle := lipgloss.NewStyle().
		PaddingLeft(1)

	selectedStyle := lipgloss.NewStyle().
		PaddingLeft(1).
		Background(lipgloss.Color("238"))

	title := titleStyle.Render(func() string {
		if m.scanning {
			// Add a simple spinner animation
			spinners := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
			spinner := spinners[int(time.Now().UnixMilli()/100)%len(spinners)]
			if m.currentWorkspace != nil {
				return fmt.Sprintf("%s Scanning '%s'... (%d found)", spinner, m.currentWorkspace.Name, len(m.repos))
			}
			return fmt.Sprintf("%s Scanning Workspaces... (%d found)", spinner, len(m.repos))
		}
		lastScan := ""
		if !m.lastScanTime.IsZero() {
			lastScan = fmt.Sprintf(" • Last scan: %s ago", formatRelativeTime(m.lastScanTime))
		}

		// Calculate workspace-specific repo count
		var workspaceRepos int
		if m.currentWorkspace != nil {
			for _, repo := range m.repos {
				if repo.WorkspaceName == m.currentWorkspace.Name {
					workspaceRepos++
				}
			}
		} else {
			workspaceRepos = len(m.repos)
		}

		displayedRepos := len(m.filteredRepos)

		if m.currentWorkspace != nil {
			if m.filterText != "" {
				return fmt.Sprintf("📂 %s (%d/%d repos)%s", m.currentWorkspace.Name, displayedRepos, workspaceRepos, lastScan)
			}
			return fmt.Sprintf("📂 %s (%d repos)%s", m.currentWorkspace.Name, workspaceRepos, lastScan)
		}
		if m.filterText != "" {
			return fmt.Sprintf("📁 All Repositories (%d/%d)%s", displayedRepos, workspaceRepos, lastScan)
		}
		return fmt.Sprintf("📁 All Repositories (%d)%s", workspaceRepos, lastScan)
	}())

	content := []string{title, ""}

	// Initialize filtered repos if needed
	if len(m.filteredRepos) == 0 && len(m.repos) > 0 {
		m.updateFilteredRepos()
	}

	// Show search mode or filter text if active
	if m.searchMode {
		searchStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
		cursor := ""
		if time.Now().UnixMilli()/500%2 == 0 {
			cursor = "█"
		} else {
			cursor = "_"
		}
		content = append(content, searchStyle.Render(fmt.Sprintf("Search: %s%s", m.filterText, cursor)))
		content = append(content, "")
	} else if m.filterText != "" {
		filterStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
		content = append(content, filterStyle.Render(fmt.Sprintf("Filter: %s (press / to edit)", m.filterText)))
		content = append(content, "")
	}

	if len(m.filteredRepos) == 0 {
		if !m.scanning {
			if len(m.repos) == 0 {
				if m.workspaceConfig != nil && len(m.workspaceConfig.Workspaces) == 0 {
					// No workspaces configured - guide user to setup
					content = append(content, itemStyle.Render("👋 Welcome to kvist!"))
					content = append(content, itemStyle.Render(""))
					content = append(content, itemStyle.Render("No workspaces configured yet."))
					content = append(content, itemStyle.Render(""))
					content = append(content, itemStyle.Render("🎯 Press 'w' to add your first workspace"))
					content = append(content, itemStyle.Render(""))
					content = append(content, itemStyle.Render("A workspace is a directory containing your Git repositories"))
					content = append(content, itemStyle.Render("(e.g., ~/code, ~/projects, /mnt/c/code)"))
				} else {
					// Workspaces configured but no repos found
					content = append(content, itemStyle.Render("No repositories found"))
					content = append(content, itemStyle.Render(""))
					if m.currentWorkspace != nil {
						content = append(content, itemStyle.Render(fmt.Sprintf("Press 'r' to scan workspace '%s'", m.currentWorkspace.Name)))
					} else {
						content = append(content, itemStyle.Render("Press 'w' to select a workspace, then press 'r' to scan"))
					}
				}
			} else {
				// Filtering resulted in no matches
				content = append(content, itemStyle.Render("No repositories match your filter"))
				content = append(content, itemStyle.Render("Press Esc to clear filter"))
			}
		}
	} else {
		// Calculate scrolling bounds
		visibleItems := height - len(content) - 3 // Reserve space for title and margins

		// Calculate scroll window
		startIdx := 0
		if m.selectedRepo >= visibleItems {
			startIdx = m.selectedRepo - visibleItems + 1
		}
		endIdx := startIdx + visibleItems
		if endIdx > len(m.filteredRepos) {
			endIdx = len(m.filteredRepos)
		}

		currentWorkspace := ""
		displayIndex := 0

		for i, repo := range m.filteredRepos[startIdx:endIdx] {
			actualIndex := startIdx + i

			// Add workspace header if changed (only for multi-workspace view)
			if m.currentWorkspace == nil && repo.WorkspaceName != currentWorkspace {
				currentWorkspace = repo.WorkspaceName
				if displayIndex > 0 { // Add spacing between workspaces
					content = append(content, "")
				}
				content = append(content, workspaceStyle.Render("📂 "+currentWorkspace))
				displayIndex++
			}

			// Format repo line
			repoLine := fmt.Sprintf("  %s", repoNameStyle.Render(repo.Name))

			// Add branch info or loading indicator
			if repo.Branch != "" {
				repoLine += " " + branchStyle.Render("("+repo.Branch+")")

				// Add status info
				var statusParts []string
				if repo.Ahead > 0 {
					statusParts = append(statusParts, fmt.Sprintf("↑%d", repo.Ahead))
				}
				if repo.Behind > 0 {
					statusParts = append(statusParts, fmt.Sprintf("↓%d", repo.Behind))
				}
				if len(statusParts) > 0 {
					repoLine += " " + statusStyle.Render(strings.Join(statusParts, " "))
				}
			} else {
				// Show loading indicator for repos without metadata yet
				loadingStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
				repoLine += " " + loadingStyle.Render("⋯")
			}

			// Add freshness indicator
			if !repo.LastScanned.IsZero() {
				age := time.Since(repo.LastScanned)
				if age > 10*time.Minute {
					staleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
					repoLine += " " + staleStyle.Render("⚠")
				}
			}

			// Apply selection style
			var line string
			if actualIndex == m.selectedRepo {
				line = selectedStyle.Render(repoLine)
			} else {
				line = itemStyle.Render(repoLine)
			}
			content = append(content, line)
			displayIndex++
		}

		// Show scroll indicators if needed
		if startIdx > 0 || endIdx < len(m.filteredRepos) {
			scrollInfo := fmt.Sprintf("(%d-%d of %d)", startIdx+1, endIdx, len(m.filteredRepos))
			scrollStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
			content = append(content, scrollStyle.Render(scrollInfo))
		}
	}

	return panelStyle.Render(strings.Join(content, "\n"))
}

func (m model) renderRepoDetails(width, height int) string {
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

	labelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("244")).
		Bold(true)

	valueStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("252"))

	pathStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("241"))

	content := []string{titleStyle.Render("📋 Repository Details"), ""}

	if len(m.filteredRepos) == 0 || m.selectedRepo >= len(m.filteredRepos) {
		content = append(content, "No repository selected")
	} else {
		repo := m.filteredRepos[m.selectedRepo]

		content = append(content,
			labelStyle.Render("Name: ")+valueStyle.Render(repo.Name),
			labelStyle.Render("Path: ")+pathStyle.Render(repo.Path),
			labelStyle.Render("Workspace: ")+valueStyle.Render(repo.WorkspaceName),
		)

		if repo.Branch != "" {
			content = append(content, labelStyle.Render("Branch: ")+valueStyle.Render(repo.Branch))
		}

		if repo.HasUpstream {
			var statusParts []string
			if repo.Ahead > 0 {
				statusParts = append(statusParts, fmt.Sprintf("%d commit(s) ahead", repo.Ahead))
			}
			if repo.Behind > 0 {
				statusParts = append(statusParts, fmt.Sprintf("%d commit(s) behind", repo.Behind))
			}
			if len(statusParts) == 0 {
				statusParts = append(statusParts, "up to date")
			}
			content = append(content, labelStyle.Render("Status: ")+valueStyle.Render(strings.Join(statusParts, ", ")))
		}

		if !repo.LastCommitTime.IsZero() {
			content = append(content,
				labelStyle.Render("Last Commit: ")+valueStyle.Render(repo.LastCommitTime.Format("2006-01-02 15:04:05")),
				labelStyle.Render("Last Scanned: ")+valueStyle.Render(repo.LastScanned.Format("2006-01-02 15:04:05")),
			)
		}

		// Add navigation hint
		content = append(content, "",
			pathStyle.Render("Press Enter to open this repository"))
	}

	return panelStyle.Render(strings.Join(content, "\n"))
}

func (m *model) updateFilteredRepos() {
	// Start with all repos
	var candidateRepos []workspace.RepoInfo

	// First, filter by workspace if we're in a specific workspace
	if m.currentWorkspace != nil {
		for _, repo := range m.repos {
			if repo.WorkspaceName == m.currentWorkspace.Name {
				candidateRepos = append(candidateRepos, repo)
			}
		}
	} else {
		// No specific workspace selected, use all repos
		candidateRepos = m.repos
	}

	// Then apply text filtering on the workspace-filtered results
	if m.filterText == "" {
		m.filteredRepos = candidateRepos
	} else {
		m.filteredRepos = make([]workspace.RepoInfo, 0)
		filter := strings.ToLower(m.filterText)
		for _, repo := range candidateRepos {
			if strings.Contains(strings.ToLower(repo.Name), filter) ||
				strings.Contains(strings.ToLower(repo.Path), filter) {
				m.filteredRepos = append(m.filteredRepos, repo)
			}
		}
	}

	// Adjust selection if it's out of bounds
	if m.selectedRepo >= len(m.filteredRepos) {
		m.selectedRepo = len(m.filteredRepos) - 1
	}
	if m.selectedRepo < 0 {
		m.selectedRepo = 0
	}
}

// updateDirSuggestions updates directory suggestions based on current path input
func (m *model) updateDirSuggestions() {
	m.dirSuggestions = workspace.GetDirectorySuggestions(m.newWorkspacePath)
	m.selectedSuggestion = 0
}

func formatRelativeTime(t time.Time) string {
	duration := time.Since(t)
	if duration < time.Minute {
		return fmt.Sprintf("%ds", int(duration.Seconds()))
	} else if duration < time.Hour {
		return fmt.Sprintf("%dm", int(duration.Minutes()))
	} else if duration < 24*time.Hour {
		return fmt.Sprintf("%dh", int(duration.Hours()))
	} else {
		return fmt.Sprintf("%dd", int(duration.Hours()/24))
	}
}

func (m model) renderWorkspaceManager(width, height int) string {
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

	itemStyle := lipgloss.NewStyle().
		PaddingLeft(1)

	selectedStyle := lipgloss.NewStyle().
		PaddingLeft(1).
		Background(lipgloss.Color("238"))

	title := titleStyle.Render("⚙️  Workspace Management")
	content := []string{title, ""}

	if m.editingWorkspace {
		nameIndicator := "  "
		pathIndicator := "  "
		if m.editingField == 0 {
			nameIndicator = "▶ "
		} else {
			pathIndicator = "▶ "
		}

		// Add a blinking cursor to show where typing will happen
		cursor := "_"
		if time.Now().UnixMilli()/500%2 == 0 {
			cursor = "█"
		}

		// Build name field with cursor
		nameValue := m.newWorkspaceName
		if m.editingField == 0 {
			if nameValue == "" {
				nameValue = cursor
			} else {
				nameValue = nameValue + cursor
			}
		}

		// Build path field with cursor
		pathValue := m.newWorkspacePath
		if m.editingField == 1 {
			if pathValue == "" {
				pathValue = cursor
			} else {
				pathValue = pathValue + cursor
			}
		}

		// Calculate padding for alignment
		nameFieldLen := len("Name: " + m.newWorkspaceName)
		pathFieldLen := len("Path: " + m.newWorkspacePath)

		// Ensure minimum spacing before help text
		minPadding := 30
		namePadding := minPadding - nameFieldLen
		if namePadding < 4 {
			namePadding = 4
		}
		pathPadding := minPadding - pathFieldLen
		if pathPadding < 4 {
			pathPadding = 4
		}

		// Help text positioned to the right with proper spacing
		nameHelp := strings.Repeat(" ", namePadding) + "(e.g., Code, Projects, Work)"
		pathHelp := strings.Repeat(" ", pathPadding) + "(type to see suggestions)"

		content = append(content,
			"📝 Add New Workspace:",
			"",
			fmt.Sprintf("%sName: %s%s", nameIndicator, nameValue, nameHelp),
			fmt.Sprintf("%sPath: %s%s", pathIndicator, pathValue, pathHelp),
		)

		// Show directory suggestions if in path field
		if m.editingField == 1 && len(m.dirSuggestions) > 0 {
			content = append(content, "")
			suggestionStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
			selectedSuggestionStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color("214")).
				Background(lipgloss.Color("238"))

			maxVisible := 5
			totalSuggestions := len(m.dirSuggestions)

			// Calculate scroll window to keep selected item visible
			scrollOffset := 0
			if m.selectedSuggestion >= maxVisible {
				scrollOffset = m.selectedSuggestion - maxVisible + 1
			}
			endIdx := scrollOffset + maxVisible
			if endIdx > totalSuggestions {
				endIdx = totalSuggestions
			}

			// Show range indicator if there are more items
			if totalSuggestions > maxVisible {
				rangeText := fmt.Sprintf("  [%d-%d of %d]", scrollOffset+1, endIdx, totalSuggestions)
				content = append(content, suggestionStyle.Render(rangeText))
			}

			for i := scrollOffset; i < endIdx; i++ {
				suggestion := m.dirSuggestions[i]
				if i == m.selectedSuggestion {
					content = append(content, selectedSuggestionStyle.Render("  ▶ "+suggestion))
				} else {
					content = append(content, suggestionStyle.Render("    "+suggestion))
				}
			}
		}

		content = append(content,
			"",
			"Tab: autocomplete/next field • ↑↓: navigate • Enter: Save • Esc: Cancel",
		)
	} else {
		if m.workspaceConfig != nil {
			for i, ws := range m.workspaceConfig.Workspaces {
				if len(content) >= height-3 {
					break
				}

				// Count repos in this workspace
				repoCount := 0
				if m.scanner != nil {
					allRepos := m.scanner.GetCachedRepos()
					for _, repo := range allRepos {
						if repo.WorkspaceName == ws.Name {
							repoCount++
						}
					}
				}

				line := fmt.Sprintf("  📂 %s (%d repos)", ws.Name, repoCount)
				line += fmt.Sprintf(" - %s", ws.Path)

				if i == m.selectedWorkspace {
					content = append(content, selectedStyle.Render(line))
				} else {
					content = append(content, itemStyle.Render(line))
				}
			}

			// Add "Add New Workspace" option
			addNewLine := "  ➕ Add New Workspace"
			if m.selectedWorkspace == len(m.workspaceConfig.Workspaces) {
				content = append(content, selectedStyle.Render(addNewLine))
			} else {
				content = append(content, itemStyle.Render(addNewLine))
			}
		}
	}

	return panelStyle.Render(strings.Join(content, "\n"))
}

func (m model) renderWorkspaceHelp(width, height int) string {
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

	helpStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("244")).
		PaddingLeft(1)

	content := []string{titleStyle.Render("🎯 Workspace Commands"), ""}

	if m.editingWorkspace {
		content = append(content,
			helpStyle.Render("Tab: Switch between Name and Path fields"),
			helpStyle.Render("Enter: Save workspace (both fields required)"),
			helpStyle.Render("Backspace: Delete characters"),
			helpStyle.Render("Esc: Cancel without saving"),
		)
	} else {
		content = append(content,
			helpStyle.Render("↑↓/jk: Navigate workspaces"),
			helpStyle.Render("Space/Enter: Open workspace/Add new"),
			helpStyle.Render("d: Delete workspace"),
			helpStyle.Render("w: Back to workspace view"),
			helpStyle.Render("q: Quit"),
		)
	}

	return panelStyle.Render(strings.Join(content, "\n"))
}

func main() {
	p := tea.NewProgram(initialModel(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error running program: %v", err)
		os.Exit(1)
	}
}
