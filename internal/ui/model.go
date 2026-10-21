// Package ui is the Bubble Tea front end of dockerterm.
package ui

import (
	"context"
	"errors"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/kushbhardwaj28/dockerterm/internal/docker"
)

// Backend is the part of the Docker client the UI needs. *docker.Client
// implements it; tests use a fake.
type Backend interface {
	Info(ctx context.Context) (docker.Info, error)
	ListContainers(ctx context.Context) ([]docker.Container, error)
	Stats(ctx context.Context, ids []string) (map[string]docker.Stats, error)
	ListImages(ctx context.Context) ([]docker.Image, error)
	ListVolumes(ctx context.Context) ([]docker.Volume, error)
	ListNetworks(ctx context.Context) ([]docker.Network, error)
	DiskUsage(ctx context.Context) (docker.DiskUsage, error)
	InspectContainer(ctx context.Context, id string) (docker.ContainerDetail, error)
	Logs(ctx context.Context, id string, tail int) ([]docker.LogLine, error)

	StartContainer(ctx context.Context, id string) error
	StopContainer(ctx context.Context, id string) error
	RestartContainer(ctx context.Context, id string) error
	PauseContainer(ctx context.Context, id string) error
	UnpauseContainer(ctx context.Context, id string) error
	RemoveContainer(ctx context.Context, id string, force bool) error
	RemoveImage(ctx context.Context, ref string) error
	RemoveVolume(ctx context.Context, name string) error
	RemoveNetwork(ctx context.Context, id string) error
}

// Options configure the UI.
type Options struct {
	Endpoint        string        // shown in the header, e.g. "colima · unix:///…"
	DockerHost      string        // passed to `docker exec` as DOCKER_HOST when set
	RefreshInterval time.Duration // default 2s
	RequestTimeout  time.Duration // default 10s
}

type viewKind int

const (
	viewContainers viewKind = iota
	viewImages
	viewVolumes
	viewNetworks
	numViews
)

var viewNames = [numViews]string{"containers", "images", "volumes", "networks"}

type detailTab int

const (
	tabInfo detailTab = iota
	tabLogs
	tabStats
	tabEnv
	tabMounts
	tabInspect
	numTabs
)

var tabNames = [numTabs]string{"Info", "Logs", "Stats", "Env", "Mounts", "Inspect"}

type stateFilter int

const (
	filterAll stateFilter = iota
	filterRunning
	filterStopped
)

var filterNames = []string{"all", "running", "stopped"}

const (
	logTail     = 200
	historySize = 60
	diskEvery   = 30 * time.Second
)

type toastKind int

const (
	toastInfo toastKind = iota
	toastOK
	toastWarn
	toastErr
)

type confirmState struct {
	title, body, cmd, verb string
	run                    tea.Cmd
}

// Model is the root Bubble Tea model.
type Model struct {
	backend Backend
	opts    Options
	keys    keyMap

	// data
	info       docker.Info
	infoAsked  bool
	containers []docker.Container
	stats      map[string]docker.Stats
	cpuHist    map[string][]float64
	images     []docker.Image
	volumes    []docker.Volume
	networks   []docker.Network
	disk       *docker.DiskUsage
	detail     *docker.ContainerDetail
	logs       []docker.LogLine
	logsFor    string
	lastUpdate time.Time

	// refresh state
	loaded      bool
	refreshErr  error
	statsFailed bool
	refreshing  bool
	refreshNext bool // an action finished mid-refresh; refresh again right away
	tickGen     int
	diskAsked   time.Time

	// navigation
	view        viewKind
	cursor      [numViews]int
	offset      [numViews]int
	filter      stateFilter
	query       textinput.Model
	tab         detailTab
	detailTop   int // scroll offset inside the detail tab
	detailsOpen bool
	zoom        bool // details pane takes the whole width
	reveal      bool // show secret-looking env values for the selected container

	// overlays and messages
	help      bool
	confirm   *confirmState
	toast     string
	toastKind toastKind

	width, height int
}

// New builds the model. Run it with tea.NewProgram(ui.New(...)).Run().
func New(backend Backend, opts Options) Model {
	if opts.RefreshInterval <= 0 {
		opts.RefreshInterval = 2 * time.Second
	}
	if opts.RequestTimeout <= 0 {
		opts.RequestTimeout = 10 * time.Second
	}
	q := textinput.New()
	q.Prompt = ""
	q.Placeholder = "filter by name or image"
	q.CharLimit = 64
	return Model{
		backend:     backend,
		opts:        opts,
		keys:        defaultKeys(),
		stats:       map[string]docker.Stats{},
		cpuHist:     map[string][]float64{},
		query:       q,
		detailsOpen: true,
		toast:       "connecting to docker…",
	}
}

// Messages.
type (
	refreshMsg struct {
		containers []docker.Container
		stats      map[string]docker.Stats
		images     []docker.Image
		volumes    []docker.Volume
		networks   []docker.Network
		listErr    error // containers could not be listed
		statsErr   error // some stats failed
		otherErr   error // images, volumes or networks failed
		at         time.Time
	}
	tickMsg   struct{ gen int }
	infoMsg   struct{ info docker.Info }
	diskMsg   struct{ du docker.DiskUsage }
	detailMsg struct {
		detail docker.ContainerDetail
		err    error
	}
	logsMsg struct {
		id    string
		lines []docker.LogLine
		err   error
	}
	actionMsg struct {
		done string
		err  error
	}
)

func (m Model) Init() tea.Cmd { return m.refreshCmd() }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case tea.WindowSizeMsg:
		if m.width == 0 && msg.Width < 100 {
			m.detailsOpen = false
		}
		m.width, m.height = msg.Width, msg.Height
		m.clampCursor()
		return m, nil

	case tickMsg:
		if msg.gen != m.tickGen || m.refreshing {
			return m, nil
		}
		m.refreshing = true
		return m, m.refreshCmd()

	case refreshMsg:
		return m.applyRefresh(msg)

	case infoMsg:
		m.info = msg.info
		m.infoAsked = msg.info.Version != ""
		return m, nil

	case diskMsg:
		du := msg.du
		m.disk = &du
		return m, nil

	case detailMsg:
		if msg.err == nil {
			if c := m.selectedContainer(); c != nil && c.ID == msg.detail.ID {
				d := msg.detail
				m.detail = &d
			}
		}
		return m, nil

	case logsMsg:
		if c := m.selectedContainer(); c != nil && c.ID == msg.id {
			if msg.err != nil {
				m.setToast(msg.err.Error(), toastErr)
			} else {
				m.logs, m.logsFor = msg.lines, msg.id
			}
		}
		return m, nil

	case actionMsg:
		if msg.err != nil {
			m.setToast(msg.err.Error(), toastErr)
		} else {
			m.setToast(msg.done, toastOK)
		}
		return m, m.refreshNow()
	}
	return m, nil
}

func (m *Model) setToast(s string, kind toastKind) { m.toast, m.toastKind = s, kind }

// applyRefresh merges a refresh result, keeping the selection on the same
// item even when the order changes.
func (m Model) applyRefresh(msg refreshMsg) (tea.Model, tea.Cmd) {
	m.refreshing = false
	m.tickGen++
	gen := m.tickGen
	delay := m.opts.RefreshInterval
	if m.refreshNext {
		m.refreshNext, delay = false, time.Millisecond
	}
	cmds := []tea.Cmd{tea.Tick(delay, func(time.Time) tea.Msg { return tickMsg{gen: gen} })}

	if msg.listErr != nil {
		m.refreshErr = msg.listErr
		m.statsFailed = false
		m.stats = map[string]docker.Stats{}
		return m, tea.Batch(cmds...)
	}

	before := m.selectedKey()
	m.containers = msg.containers
	m.stats = msg.stats
	if m.stats == nil {
		m.stats = map[string]docker.Stats{}
	}
	m.recordHistory()
	if msg.otherErr == nil {
		m.images, m.volumes, m.networks = msg.images, msg.volumes, msg.networks
	}
	if !m.loaded && m.toast == "connecting to docker…" {
		m.toast = ""
	}
	m.loaded = true
	m.lastUpdate = msg.at
	m.refreshErr = errors.Join(msg.statsErr, msg.otherErr)
	m.statsFailed = msg.statsErr != nil
	m.reselect(before)

	if !m.infoAsked {
		m.infoAsked = true
		cmds = append(cmds, m.infoCmd())
	}
	if m.diskAsked.IsZero() || msg.at.Sub(m.diskAsked) >= diskEvery {
		m.diskAsked = msg.at
		cmds = append(cmds, m.diskCmd())
	}
	cmds = append(cmds, m.selectionCmds()...)
	return m, tea.Batch(cmds...)
}

func (m *Model) recordHistory() {
	live := map[string]bool{}
	for _, c := range m.containers {
		live[c.ID] = true
		if s, ok := m.stats[c.ID]; ok {
			h := append(m.cpuHist[c.ID], s.CPUPercent)
			if len(h) > historySize {
				h = h[len(h)-historySize:]
			}
			m.cpuHist[c.ID] = h
		}
	}
	for id := range m.cpuHist {
		if !live[id] {
			delete(m.cpuHist, id)
		}
	}
}

// selectionCmds fetches what the detail pane needs for the selected container.
func (m Model) selectionCmds() []tea.Cmd {
	c := m.selectedContainer()
	if m.view != viewContainers || c == nil {
		return nil
	}
	cmds := []tea.Cmd{m.inspectCmd(c.ID)}
	if m.tab == tabLogs {
		cmds = append(cmds, m.logsCmd(c.ID))
	}
	return cmds
}

// selectionChanged resets per-container state after the cursor moved.
func (m *Model) selectionChanged(before string) tea.Cmd {
	if m.selectedKey() == before {
		return nil
	}
	m.reveal = false // secrets are revealed per container, never carried over
	m.detail, m.logs, m.logsFor, m.detailTop = nil, nil, "", 0
	return tea.Batch(m.selectionCmds()...)
}

func (m *Model) refreshNow() tea.Cmd {
	if m.refreshing {
		m.refreshNext = true // the result in flight predates this change
		return nil
	}
	m.refreshing = true
	m.tickGen++ // drop the pending tick; applyRefresh schedules the next one
	return m.refreshCmd()
}

func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "dockterm"
	return v
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := m.keys
	switch {
	case m.confirm != nil:
		switch {
		case key.Matches(msg, k.Confirm):
			run := m.confirm.run
			m.confirm = nil
			return m, run
		case key.Matches(msg, k.Cancel):
			m.confirm = nil
			m.setToast("cancelled", toastWarn)
		}
		return m, nil

	case m.help:
		if key.Matches(msg, k.Cancel, k.Help) {
			m.help = false
		}
		return m, nil

	case m.query.Focused():
		before := m.selectedKey()
		switch msg.String() {
		case "esc":
			m.query.SetValue("")
			m.query.Blur()
		case "enter":
			m.query.Blur()
		default:
			var cmd tea.Cmd
			m.query, cmd = m.query.Update(msg)
			m.cursor[m.view], m.offset[m.view] = 0, 0
			return m, tea.Batch(cmd, m.selectionChanged(before))
		}
		m.clampCursor()
		return m, m.selectionChanged(before)
	}

	before := m.selectedKey()
	switch {
	case key.Matches(msg, k.Quit):
		return m, tea.Quit
	case key.Matches(msg, k.Help):
		m.help = true
	case key.Matches(msg, k.Refresh):
		return m, m.refreshNow()

	case key.Matches(msg, k.Containers, k.Images, k.Volumes, k.Networks):
		m.view = viewKind(msg.String()[0] - '1')
		m.query.SetValue("")
		m.clampCursor()
	case key.Matches(msg, k.Filter):
		return m, m.query.Focus()
	case key.Matches(msg, k.Clear):
		if m.zoom && m.query.Value() == "" {
			m.zoom = false
		}
		m.query.SetValue("")
		m.clampCursor()
	case key.Matches(msg, k.CycleFilter) && m.view == viewContainers:
		m.filter = (m.filter + 1) % stateFilter(len(filterNames))
		m.cursor[m.view] = 0
		m.clampCursor()

	case key.Matches(msg, k.Up):
		m.move(-1)
	case key.Matches(msg, k.Down):
		m.move(1)
	case key.Matches(msg, k.Top):
		m.move(-1 << 20)
	case key.Matches(msg, k.Bottom):
		m.move(1 << 20)
	case key.Matches(msg, k.PageUp):
		m.move(-m.listRows())
	case key.Matches(msg, k.PageDn):
		m.move(m.listRows())

	case key.Matches(msg, k.Details):
		switch {
		case m.zoom:
			m.zoom = false
		case m.rowCount() > 0:
			m.detailsOpen = !m.detailsOpen
		}
	case key.Matches(msg, k.Zoom):
		if m.zoom || m.rowCount() > 0 {
			m.zoom = !m.zoom
			m.detailsOpen = true
			m.detailTop = 0
		}
	case key.Matches(msg, k.Reveal) && m.view == viewContainers:
		m.reveal = !m.reveal
		if m.reveal {
			m.setToast("secret values visible for "+m.selectedKeyName()+" — v to hide", toastWarn)
		} else {
			m.setToast("secret values hidden", toastInfo)
		}
	case key.Matches(msg, k.NextTab) && m.view == viewContainers:
		return m, m.setTab((m.tab + 1) % numTabs)
	case key.Matches(msg, k.PrevTab) && m.view == viewContainers:
		return m, m.setTab((m.tab + numTabs - 1) % numTabs)
	case key.Matches(msg, k.ScrollDown):
		m.detailTop += 3
	case key.Matches(msg, k.ScrollUp):
		m.detailTop = max(0, m.detailTop-3)

	case key.Matches(msg, k.Logs) && m.view == viewContainers:
		return m, m.setTab(tabLogs)
	case key.Matches(msg, k.Stats) && m.view == viewContainers:
		return m, m.setTab(tabStats)
	case key.Matches(msg, k.Inspect) && m.view == viewContainers:
		return m, m.setTab(tabInspect)

	case key.Matches(msg, k.Start, k.Stop, k.Restart, k.Pause, k.Remove, k.Exec):
		return m, m.action(msg)
	}
	return m, m.selectionChanged(before)
}

func (m *Model) setTab(t detailTab) tea.Cmd {
	m.tab, m.detailTop = t, 0
	if !m.detailsOpen {
		m.detailsOpen = true
	}
	if c := m.selectedContainer(); c != nil && t == tabLogs {
		return m.logsCmd(c.ID)
	}
	return nil
}

func (m *Model) move(delta int) {
	m.cursor[m.view] += delta
	m.clampCursor()
}

func (m *Model) clampCursor() {
	n := m.rowCount()
	c := &m.cursor[m.view]
	*c = min(max(0, *c), max(0, n-1))
	// keep the cursor on screen
	rows := m.listRows()
	off := &m.offset[m.view]
	if *c < *off {
		*off = *c
	}
	if rows > 0 && *c >= *off+rows {
		*off = *c - rows + 1
	}
	*off = min(*off, max(0, n-rows))
}

// Commands.

func (m Model) ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), m.opts.RequestTimeout)
}

func (m Model) refreshCmd() tea.Cmd {
	return func() tea.Msg {
		// each call gets its own deadline so one slow endpoint cannot starve the rest
		call := func(fn func(ctx context.Context)) {
			ctx, cancel := m.ctx()
			defer cancel()
			fn(ctx)
		}
		var containers []docker.Container
		var err error
		call(func(ctx context.Context) { containers, err = m.backend.ListContainers(ctx) })
		if err != nil {
			return refreshMsg{listErr: err, at: time.Now()}
		}
		var running []string
		for _, c := range containers {
			if c.Running() {
				running = append(running, c.ID)
			}
		}
		var (
			stats            map[string]docker.Stats
			images           []docker.Image
			volumes          []docker.Volume
			networks         []docker.Network
			statsErr, e1, e2 error
			e3               error
		)
		call(func(ctx context.Context) { stats, statsErr = m.backend.Stats(ctx, running) })
		call(func(ctx context.Context) { images, e1 = m.backend.ListImages(ctx) })
		call(func(ctx context.Context) { volumes, e2 = m.backend.ListVolumes(ctx) })
		call(func(ctx context.Context) { networks, e3 = m.backend.ListNetworks(ctx) })
		return refreshMsg{
			containers: containers, stats: stats, statsErr: statsErr,
			images: images, volumes: volumes, networks: networks,
			otherErr: errors.Join(e1, e2, e3), at: time.Now(),
		}
	}
}

func (m Model) infoCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := m.ctx()
		defer cancel()
		info, _ := m.backend.Info(ctx)
		return infoMsg{info: info}
	}
}

func (m Model) diskCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		du, err := m.backend.DiskUsage(ctx)
		if err != nil {
			return nil
		}
		return diskMsg{du: du}
	}
}

func (m Model) inspectCmd(id string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := m.ctx()
		defer cancel()
		d, err := m.backend.InspectContainer(ctx, id)
		return detailMsg{detail: d, err: err}
	}
}

func (m Model) logsCmd(id string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := m.ctx()
		defer cancel()
		lines, err := m.backend.Logs(ctx, id, logTail)
		return logsMsg{id: id, lines: lines, err: err}
	}
}

// selectedKeyName is the selected container's name, for messages.
func (m Model) selectedKeyName() string {
	if c := m.selectedContainer(); c != nil {
		return c.Name
	}
	return "this container"
}
