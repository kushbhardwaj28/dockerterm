// Package ui is the Bubble Tea front end of dockerterm.
package ui

import (
	"context"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"

	"github.com/kushbhardwaj28/dockerterm/internal/docker"
)

// Backend is the part of the Docker client the UI needs. *docker.Client
// implements it; tests use a fake.
type Backend interface {
	ListContainers(ctx context.Context) ([]docker.Container, error)
	Stats(ctx context.Context, ids []string) (map[string]docker.Stats, error)
	ServerVersion(ctx context.Context) (string, error)
}

// Options configure the UI.
type Options struct {
	Endpoint        string        // shown in the header, e.g. "colima · unix:///…"
	RefreshInterval time.Duration // default 2s
	RequestTimeout  time.Duration // default 10s; stats need ~1s per sample
}

// Model is the root Bubble Tea model.
type Model struct {
	backend Backend
	opts    Options
	keys    keyMap

	containers    []docker.Container
	stats         map[string]docker.Stats
	engineVersion string
	versionAsked  bool

	loaded      bool  // at least one successful list
	refreshErr  error // last list error, or stats error when statsFailed
	statsFailed bool  // list worked but some stats did not
	refreshing  bool
	tickGen     int // ticks from older generations are ignored

	table       table.Model
	width       int
	height      int
	detailsOpen bool
}

// New builds the model. Call tea.NewProgram(ui.New(...)).Run() to start it.
func New(backend Backend, opts Options) Model {
	if opts.RefreshInterval <= 0 {
		opts.RefreshInterval = 2 * time.Second
	}
	if opts.RequestTimeout <= 0 {
		opts.RequestTimeout = 10 * time.Second
	}
	return Model{
		backend: backend,
		opts:    opts,
		keys:    defaultKeyMap(),
		table:   newContainerTable(),
		stats:   map[string]docker.Stats{},
	}
}

// Messages.
type (
	refreshMsg struct {
		containers []docker.Container
		stats      map[string]docker.Stats
		listErr    error
		statsErr   error
	}
	tickMsg    struct{ gen int }
	versionMsg struct{ version string }
)

func (m Model) Init() tea.Cmd {
	return m.refreshCmd()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case tea.WindowSizeMsg:
		if m.width == 0 && msg.Width < 100 {
			m.detailsOpen = false
		}
		m.width, m.height = msg.Width, msg.Height
		m.resize()
		return m, nil

	case tickMsg:
		if msg.gen != m.tickGen || m.refreshing {
			return m, nil
		}
		m.refreshing = true
		return m, m.refreshCmd()

	case refreshMsg:
		m.applyRefresh(msg)
		m.refreshing = false
		m.tickGen++
		gen := m.tickGen
		next := tea.Tick(m.opts.RefreshInterval, func(time.Time) tea.Msg { return tickMsg{gen: gen} })
		if msg.listErr == nil && m.engineVersion == "" && !m.versionAsked {
			// Ask once the daemon is known to answer.
			m.versionAsked = true
			return m, tea.Batch(next, m.versionCmd())
		}
		return m, next

	case versionMsg:
		m.engineVersion = msg.version
		m.versionAsked = msg.version != "" // retry after the next refresh if it failed
		return m, nil
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit
	case key.Matches(msg, m.keys.Details):
		if len(m.containers) > 0 {
			m.detailsOpen = !m.detailsOpen
			m.resize()
		}
		return m, nil
	case key.Matches(msg, m.keys.Refresh):
		if m.refreshing {
			return m, nil
		}
		m.refreshing = true
		m.tickGen++ // drop the pending tick; a new one is scheduled after this refresh
		return m, m.refreshCmd()
	}
	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

// applyRefresh merges a refresh result, keeping the selection on the same
// container even when the list order changes.
func (m *Model) applyRefresh(msg refreshMsg) {
	if msg.listErr != nil {
		// Keep the last list on screen but drop metrics we can no longer vouch for.
		m.refreshErr = msg.listErr
		m.statsFailed = false
		m.stats = map[string]docker.Stats{}
		m.table.SetRows(containerRows(m.containers, m.stats))
		return
	}

	selectedID := ""
	if c := m.selectedContainer(); c != nil {
		selectedID = c.ID
	}
	m.containers = msg.containers
	m.stats = msg.stats
	if m.stats == nil {
		m.stats = map[string]docker.Stats{}
	}
	m.loaded = true
	m.refreshErr = msg.statsErr
	m.statsFailed = msg.statsErr != nil

	m.table.SetRows(containerRows(m.containers, m.stats))
	cursor := 0
	for i, c := range m.containers {
		if c.ID == selectedID {
			cursor = i
			break
		}
	}
	m.table.SetCursor(cursor)
	if len(m.containers) == 0 {
		m.detailsOpen = false
	}
}

func (m Model) selectedContainer() *docker.Container {
	i := m.table.Cursor()
	if i < 0 || i >= len(m.containers) {
		return nil
	}
	return &m.containers[i]
}

func (m *Model) resize() {
	size := m.layoutSize()
	resizeTable(&m.table, size.tableWidth, max(2, size.bodyHeight-1))
}

func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "dockerterm"
	return v
}

// Commands.

func (m Model) refreshCmd() tea.Cmd {
	backend, timeout := m.backend, m.opts.RequestTimeout
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		containers, err := backend.ListContainers(ctx)
		if err != nil {
			return refreshMsg{listErr: err}
		}
		var running []string
		for _, c := range containers {
			if c.Running() {
				running = append(running, c.ID)
			}
		}
		stats, statsErr := backend.Stats(ctx, running)
		return refreshMsg{containers: containers, stats: stats, statsErr: statsErr}
	}
}

func (m Model) versionCmd() tea.Cmd {
	backend, timeout := m.backend, m.opts.RequestTimeout
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		v, _ := backend.ServerVersion(ctx)
		return versionMsg{version: v}
	}
}
