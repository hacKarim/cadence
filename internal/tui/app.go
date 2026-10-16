package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/hacKarim/cadence/internal/sandbox"
	"github.com/hacKarim/cadence/internal/store"
)

type view int

const (
	viewRuns view = iota
	viewGraph
	viewLogs
)

type tickMsg time.Time

// Model is the Bubble Tea state. It holds no rendering logic of its own: the
// pure functions in this package do that, which is why they are testable.
type Model struct {
	st       *store.Store
	backend  sandbox.Backend
	enforced bool

	view          view
	runs          []store.Run
	tree          *store.RunTree
	runSel        int
	stepSel       int
	width, height int
	err           error
}

// New builds the model.
func New(st *store.Store, backend sandbox.Backend, enforced bool) Model {
	return Model{st: st, backend: backend, enforced: enforced, width: 80, height: 24}
}

// Init starts the refresh ticker.
func (m Model) Init() tea.Cmd { return tea.Batch(tick(), m.reload) }

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

type loadedMsg struct {
	runs []store.Run
	tree *store.RunTree
	err  error
}

func (m Model) reload() tea.Msg {
	runs, err := m.st.ListRuns("", 50)
	if err != nil {
		return loadedMsg{err: err}
	}
	var tree *store.RunTree
	if m.view != viewRuns && m.runSel < len(runs) {
		tree, err = m.st.RunTree(runs[m.runSel].ID)
		if err != nil {
			return loadedMsg{runs: runs, err: err}
		}
	}
	return loadedMsg{runs: runs, tree: tree}
}

// Update handles input and refresh messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tickMsg:
		return m, tea.Batch(tick(), m.reload)
	case loadedMsg:
		m.runs, m.err = msg.runs, msg.err
		if msg.tree != nil {
			m.tree = msg.tree
		}
	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m Model) key(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "up", "k":
		m.moveSelection(-1)
	case "down", "j":
		m.moveSelection(1)
	case "enter":
		switch m.view {
		case viewRuns:
			m.view, m.stepSel = viewGraph, 0
			return m, m.reload
		case viewGraph:
			if m.tree != nil && len(m.tree.Steps) > 0 {
				m.view = viewLogs
			}
		}
	case "esc", "backspace":
		switch m.view {
		case viewLogs:
			m.view = viewGraph
		case viewGraph:
			m.view = viewRuns
		}
	case "r":
		return m, m.reload
	}
	return m, nil
}

func (m *Model) moveSelection(d int) {
	switch m.view {
	case viewRuns:
		m.runSel = clamp(m.runSel+d, 0, len(m.runs)-1)
	case viewGraph, viewLogs:
		if m.tree != nil {
			m.stepSel = clamp(m.stepSel+d, 0, len(m.tree.Steps)-1)
		}
	}
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// View renders the current screen.
func (m Model) View() string {
	var body string
	switch {
	case m.err != nil:
		body = "error: " + m.err.Error()
	case m.view == viewRuns:
		body = RenderRunList(m.runs, m.width, m.runSel)
	case m.tree == nil:
		body = "loading..."
	case m.view == viewGraph:
		body = RenderGraph(m.tree, m.width, m.backend, m.enforced)
	default:
		body = RenderLogs(m.tree.Steps[clamp(m.stepSel, 0, len(m.tree.Steps)-1)], m.width)
	}
	return body + "\n\n" + clip(styleDim.Render(m.help()), m.width) + "\n"
}

func (m Model) help() string {
	switch m.view {
	case viewRuns:
		return "j/k move  enter open  r refresh  q quit"
	case viewGraph:
		return "j/k step  enter logs  esc back  q quit"
	default:
		return "j/k step  esc back  q quit"
	}
}

// Run starts the interactive interface.
func Run(st *store.Store, backend sandbox.Backend, enforced bool) error {
	p := tea.NewProgram(New(st, backend, enforced), tea.WithAltScreen())
	_, err := p.Run()
	return err
}
