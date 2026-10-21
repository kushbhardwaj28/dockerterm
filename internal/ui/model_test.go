package ui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/kushbhardwaj28/dockerterm/internal/docker"
)

// fakeBackend records the calls the UI makes.
type fakeBackend struct {
	mu         sync.Mutex
	containers []docker.Container
	stats      map[string]docker.Stats
	images     []docker.Image
	volumes    []docker.Volume
	networks   []docker.Network
	logs       []docker.LogLine
	live       chan []docker.LogLine
	logQueries []docker.LogQuery
	listErr    error
	statsIDs   []string
	calls      []string
}

func (f *fakeBackend) record(s string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, s)
	return nil
}

func (f *fakeBackend) Info(context.Context) (docker.Info, error) {
	return docker.Info{Version: "29.4.3", NCPU: 4, MemTotal: 8 << 30}, nil
}
func (f *fakeBackend) ListContainers(context.Context) ([]docker.Container, error) {
	return f.containers, f.listErr
}
func (f *fakeBackend) Stats(_ context.Context, ids []string) (map[string]docker.Stats, error) {
	f.statsIDs = ids
	return f.stats, nil
}
func (f *fakeBackend) ListImages(context.Context) ([]docker.Image, error)     { return f.images, nil }
func (f *fakeBackend) ListVolumes(context.Context) ([]docker.Volume, error)   { return f.volumes, nil }
func (f *fakeBackend) ListNetworks(context.Context) ([]docker.Network, error) { return f.networks, nil }
func (f *fakeBackend) DiskUsage(context.Context) (docker.DiskUsage, error) {
	return docker.DiskUsage{Images: 4 << 30, Volumes: 2 << 30}, nil
}
func (f *fakeBackend) InspectContainer(_ context.Context, id string) (docker.ContainerDetail, error) {
	return docker.ContainerDetail{ID: id, Env: []string{"NODE_ENV=production", "DB_PASSWORD=hunter2"}, RestartPolicy: "always",
		Raw: "{\n  \"Env\": [\n    \"NODE_ENV=production\",\n    \"DB_PASSWORD=hunter2\"\n  ]\n}"}, nil
}

// Logs behaves like the daemon: tail is applied to the whole log first, then
// since/until (both inclusive) filter what is left.
func (f *fakeBackend) Logs(_ context.Context, _ string, q docker.LogQuery) ([]docker.LogLine, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logQueries = append(f.logQueries, q)
	all := f.logs
	if q.Tail > 0 && len(all) > q.Tail {
		all = all[len(all)-q.Tail:]
	}
	var out []docker.LogLine
	for _, l := range all {
		if (!q.Since.IsZero() && l.Time.Before(q.Since)) || (!q.Until.IsZero() && l.Time.After(q.Until)) {
			continue
		}
		out = append(out, l)
	}
	return out, nil
}

// FollowLogs sends the tail of f.logs, then whatever is pushed on f.live,
// until the stream is cancelled or f.live is closed (container stopped).
func (f *fakeBackend) FollowLogs(ctx context.Context, id string, q docker.LogQuery, emit func([]docker.LogLine)) error {
	first, _ := f.Logs(ctx, id, docker.LogQuery{Tail: q.Tail})
	if q.Since.IsZero() {
		emit(first)
	}
	if f.live == nil {
		<-ctx.Done()
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case batch, ok := <-f.live:
			if !ok {
				return nil
			}
			emit(batch)
		}
	}
}
func (f *fakeBackend) StartContainer(_ context.Context, id string) error {
	return f.record("start " + id)
}
func (f *fakeBackend) StopContainer(_ context.Context, id string) error {
	return f.record("stop " + id)
}
func (f *fakeBackend) RestartContainer(_ context.Context, id string) error {
	return f.record("restart " + id)
}
func (f *fakeBackend) PauseContainer(_ context.Context, id string) error {
	return f.record("pause " + id)
}
func (f *fakeBackend) UnpauseContainer(_ context.Context, id string) error {
	return f.record("unpause " + id)
}
func (f *fakeBackend) RemoveContainer(_ context.Context, id string, force bool) error {
	if force {
		return f.record("rm -f " + id)
	}
	return f.record("rm " + id)
}
func (f *fakeBackend) RemoveImage(_ context.Context, ref string) error { return f.record("rmi " + ref) }
func (f *fakeBackend) RemoveVolume(_ context.Context, n string) error {
	return f.record("volume rm " + n)
}
func (f *fakeBackend) RemoveNetwork(_ context.Context, id string) error {
	return f.record("network rm " + id)
}

func running(id, name string) docker.Container {
	return docker.Container{ID: id, Name: name, Image: "myorg/" + name, State: "running", Status: "Up 3 days"}
}

func sample() refreshMsg {
	return refreshMsg{
		containers: []docker.Container{
			{ID: "aaa", Name: "web-nginx", Image: "nginx:1.27", ImageID: "img1", State: "running", Status: "Up 3 days",
				Networks: []string{"app_frontend"}, Volumes: []string{"static_assets"},
				Labels: map[string]string{"com.docker.compose.project": "app"}},
			{ID: "bbb", Name: "api-server", Image: "myorg/api:2.4.1", State: "running", Status: "Up 3 days", Networks: []string{"app_backend"}},
			{ID: "ccc", Name: "worker-queue", Image: "myorg/worker", State: "paused", Status: "Up 2 days (Paused)"},
			{ID: "ddd", Name: "db-migrate", Image: "myorg/api:2.4.1", State: "exited", Status: "Exited (0) 3 days ago"},
		},
		stats: map[string]docker.Stats{
			"aaa": {CPUPercent: 0.4, MemoryUsage: 18 << 20, MemoryLimit: 512 << 20},
			"bbb": {CPUPercent: 12.6, MemoryUsage: 342 << 20, MemoryLimit: 1 << 30},
		},
		images: []docker.Image{
			{ID: "img1", Repo: "nginx", Tag: "1.27", Size: 47 << 20},
			{ID: "img2", Repo: "<none>", Tag: "<none>", Size: 380 << 20},
		},
		volumes:  []docker.Volume{{Name: "orphan", Driver: "local", Size: -1}, {Name: "static_assets", Driver: "local", Size: -1}},
		networks: []docker.Network{{ID: "n1", Name: "app_frontend", Driver: "bridge", Scope: "local", Subnet: "172.20.0.0/16"}, {ID: "n2", Name: "bridge", Driver: "bridge"}},
		at:       time.Date(2026, 10, 20, 10, 41, 25, 0, time.Local),
	}
}

func newModel(b *fakeBackend) Model {
	if b == nil {
		b = &fakeBackend{}
	}
	return New(b, Options{Endpoint: "default · unix:///var/run/docker.sock"})
}

func press(s string) tea.KeyPressMsg {
	switch s {
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "ctrl+r":
		return tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl}
	}
	r := []rune(s)[0]
	return tea.KeyPressMsg{Code: r, Text: s}
}

// step feeds messages in order and returns the model plus the last command.
func step(t *testing.T, m tea.Model, msgs ...tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	var cmd tea.Cmd
	for _, msg := range msgs {
		m, cmd = m.Update(msg)
	}
	return m.(Model), cmd
}

func screen(m Model) string { return ansi.Strip(m.View().Content) }

func assertContains(t *testing.T, s string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(s, w) {
			t.Errorf("screen missing %q", w)
		}
	}
}

func assertSize(t *testing.T, m Model, w, h int) {
	t.Helper()
	c := m.View().Content
	if gw, gh := lipgloss.Width(c), lipgloss.Height(c); gw != w || gh != h {
		t.Errorf("view size = %dx%d, want %dx%d", gw, gh, w, h)
	}
}

// drain runs a command (and batches) and returns the messages it produces.
func drain(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, drain(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

func TestWideLayoutMatchesDesign(t *testing.T) {
	m, _ := step(t, newModel(nil), tea.WindowSizeMsg{Width: 160, Height: 40}, sample(), infoMsg{docker.Info{Version: "29.4.3", MemTotal: 8 << 30}}, press("down"))
	s := screen(m)
	assertContains(t, s,
		// header
		"dockterm", "ctx default · unix:///var/run/docker.sock", "engine 29.4.3", "running 2", "stopped 1", "paused 1", "cpu 13.0%", "mem 360 MB / 8 GB", "? help",
		// sidebar
		"RESOURCES", "1  Containers", "2  Images", "3  Volumes", "4  Networks",
		// list
		"containers [4]", " all ", "running", "stopped", "/ filter", "NAME", "IMAGE", "STATUS", "CPU", "MEM",
		"› api-server", "● Up 3 days", "‖ Up 2 days", "■ Exited (0)", "12.6%", "342 MB",
		// detail
		"api-server   RUNNING", "x stop", "r restart", "p pause", "l logs", "e exec", "d remove",
		"Info", "Logs", "Stats", "Env", "Mounts", "Inspect", "Status", "Up 3 days",
		// status + keys
		"NORMAL", "auto-refresh 2s · 10:41:25", "s  start", "z  full width", "v  secrets", "q  quit",
	)
	assertSize(t, m, 160, 40)
}

func TestNarrowLayoutTogglesFullScreenDetails(t *testing.T) {
	m, _ := step(t, newModel(nil), tea.WindowSizeMsg{Width: 70, Height: 20}, sample())
	if s := screen(m); !strings.Contains(s, "web-nginx") || strings.Contains(s, "RUNNING") || strings.Contains(s, "RESOURCES") {
		t.Error("narrow screen should show just the list")
	}
	m, _ = step(t, m, press("enter"))
	assertContains(t, screen(m), "web-nginx   RUNNING", "compose: app")
	assertSize(t, m, 70, 20)
	m, _ = step(t, m, press("enter"))
	assertContains(t, screen(m), "NAME")
	assertSize(t, m, 70, 20)
}

func TestRefreshKeepsSelectionAndRecordsHistory(t *testing.T) {
	m, _ := step(t, newModel(nil), sample(), press("down"))
	if c := m.selectedContainer(); c == nil || c.ID != "bbb" {
		t.Fatalf("selected = %v", c)
	}
	next := sample()
	next.containers = append([]docker.Container{running("new", "fresh")}, next.containers...)
	m, cmd := step(t, m, next)
	if c := m.selectedContainer(); c == nil || c.ID != "bbb" {
		t.Errorf("selection jumped to %v after a container was added above it", c)
	}
	if cmd == nil || len(m.cpuHist["bbb"]) != 2 {
		t.Errorf("cpu history = %v, want two samples", m.cpuHist["bbb"])
	}
}

func TestRefreshCmdOnlyAsksStatsForRunningContainers(t *testing.T) {
	b := &fakeBackend{containers: sample().containers}
	msg := newModel(b).refreshCmd()().(refreshMsg)
	if len(msg.containers) != 4 || strings.Join(b.statsIDs, ",") != "aaa,bbb" {
		t.Errorf("stats asked for %v", b.statsIDs)
	}
}

func TestFirstRefreshAsksForInfoAndDiskUsage(t *testing.T) {
	m, cmd := step(t, newModel(nil), sample())
	var gotInfo, gotDisk bool
	for _, msg := range drain(cmd) {
		switch msg.(type) {
		case infoMsg:
			gotInfo = true
		case diskMsg:
			gotDisk = true
		}
	}
	if !gotInfo || !gotDisk || !m.infoAsked {
		t.Errorf("info=%v disk=%v", gotInfo, gotDisk)
	}
}

func TestErrorsAreShownWithoutLosingTheList(t *testing.T) {
	m, _ := step(t, newModel(nil), refreshMsg{listErr: errors.New("no daemon")})
	assertContains(t, screen(m), "Docker unavailable - retrying: no daemon")

	m, _ = step(t, m, sample(), refreshMsg{listErr: errors.New("daemon went away")})
	s := screen(m)
	assertContains(t, s, "Data stale - retrying: daemon went away", "web-nginx")
	if strings.Contains(s, "12.6%") {
		t.Error("stale metrics must not be shown")
	}

	partial := sample()
	partial.statsErr = errors.New("stats bbb: timeout")
	m, _ = step(t, m, partial)
	assertContains(t, screen(m), "Stats unavailable - retrying")
}

func TestTicksAndActionsDoNotStartParallelRefreshes(t *testing.T) {
	m, _ := step(t, newModel(nil), sample())
	gen := m.tickGen
	m, cmd := step(t, m, press("ctrl+r"))
	if cmd == nil || !m.refreshing {
		t.Fatal("ctrl+r must refresh")
	}
	if _, cmd := step(t, m, tickMsg{gen: gen}); cmd != nil {
		t.Error("an old tick must not start a second refresh")
	}
	// An action finishing while a refresh is in flight asks for another one.
	m, _ = step(t, m, actionMsg{done: "stopped api-server"})
	if !m.refreshNext {
		t.Fatal("action during refresh must queue another refresh")
	}
	m, cmd = step(t, m, sample())
	if m.refreshNext || cmd == nil {
		t.Error("queued refresh must be scheduled immediately")
	}
}

func TestFilterAndStateFilter(t *testing.T) {
	m, _ := step(t, newModel(nil), tea.WindowSizeMsg{Width: 160, Height: 30}, sample(), press("/"), press("w"), press("o"), press("r"))
	if n := m.rowCount(); n != 1 {
		t.Errorf("filter 'wor' rows = %d, want 1", n)
	}
	assertContains(t, screen(m), "FILTER", "containers [1]")
	m, _ = step(t, m, press("esc"))
	if m.rowCount() != 4 || m.query.Focused() {
		t.Error("esc must clear the filter")
	}
	m, _ = step(t, m, press("f"))
	if m.rowCount() != 2 {
		t.Errorf("running filter rows = %d, want 2", m.rowCount())
	}
	m, _ = step(t, m, press("f"))
	if m.rowCount() != 2 {
		t.Errorf("stopped filter rows = %d, want 2 (paused + exited)", m.rowCount())
	}
}

func TestResourceViews(t *testing.T) {
	m, _ := step(t, newModel(nil), tea.WindowSizeMsg{Width: 160, Height: 30}, sample())
	m, _ = step(t, m, press("2"))
	assertContains(t, screen(m), "images [2]", "REPOSITORY", "nginx", "47 MB", "nginx:1.27   IN USE", "<none>")
	m, _ = step(t, m, press("3"))
	assertContains(t, screen(m), "volumes [2]", "— dangling", "orphan   DANGLING")
	m, _ = step(t, m, press("4"), press("down"))
	assertContains(t, screen(m), "networks [2]", "172.20.0.0/16", "bridge   BRIDGE")
	if _, cmd := step(t, m, press("d")); cmd != nil {
		t.Error("removing a built-in network must not run anything")
	}
}

func TestContainerActions(t *testing.T) {
	b := &fakeBackend{}
	m, _ := step(t, newModel(b), sample())

	_, cmd := step(t, m, press("x")) // web-nginx is running
	drain(cmd)
	_, cmd = step(t, m, press("s"))
	if cmd != nil {
		t.Error("starting a running container must not call docker")
	}
	m2, _ := step(t, m, press("down"), press("down")) // worker-queue (paused)
	_, cmd = step(t, m2, press("p"))
	drain(cmd)

	m3, _ := step(t, m, press("d"))
	if m3.confirm == nil || m3.confirm.cmd != "docker rm -f web-nginx" {
		t.Fatalf("remove must ask first, got %+v", m3.confirm)
	}
	assertContains(t, screen(m3), "Remove container web-nginx?", "$ docker rm -f web-nginx", "CONFIRM")
	_, cmd = step(t, m3, press("y"))
	drain(cmd)
	m4, cmd := step(t, m3, press("esc"))
	if m4.confirm != nil || cmd != nil {
		t.Error("esc must cancel without running")
	}

	if got, want := strings.Join(b.calls, "; "), "stop aaa; unpause ccc; rm -f aaa"; got != want {
		t.Errorf("docker calls = %q, want %q", got, want)
	}
}

func TestDetailTabs(t *testing.T) {
	b := &fakeBackend{logs: []docker.LogLine{
		{Time: time.Date(2026, 1, 1, 10, 41, 2, 0, time.Local), Stream: "stdout", Text: "INFO GET /api/users 200 18ms"},
		{Stream: "stderr", Text: "ERROR connection refused"},
	}}
	m, cmd := step(t, newModel(b), tea.WindowSizeMsg{Width: 160, Height: 40}, sample())
	for _, msg := range drain(cmd) {
		if _, ok := msg.(detailMsg); ok {
			m, _ = step(t, m, msg)
		}
	}
	m, _ = step(t, m, press("tab"), press("tab"), press("tab")) // Env
	assertContains(t, screen(m), "NODE_ENV=production", "DB_PASSWORD=••••••••")

	m, cmd = step(t, m, press("l"))
	if m.tab != tabLogs || cmd == nil {
		t.Fatal("l must open logs and stream them")
	}
	m = feedLogs(t, m, cmd)
	s := screen(m)
	assertContains(t, s, "docker logs -f web-nginx", "● following · 2 lines", "10:41:02 INFO  GET /api/users", "ERROR connection refused")
	if strings.Contains(s, "INFO  INFO") {
		t.Error("level must not be repeated in the message")
	}
	m, _ = step(t, m, press("t"))
	assertContains(t, screen(m), "CPU", "MEMORY", "NET I/O", "BLOCK I/O", "PIDS")
}

func TestHelpOverlay(t *testing.T) {
	m, _ := step(t, newModel(nil), tea.WindowSizeMsg{Width: 120, Height: 40}, sample(), press("?"))
	assertContains(t, screen(m), "keybindings", "switch view", "remove (asks first)", "HELP")
	m, _ = step(t, m, press("esc"))
	if m.help {
		t.Error("esc must close help")
	}
}

func TestQuit(t *testing.T) {
	_, cmd := step(t, newModel(nil), press("q"))
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("q must quit")
	}
}

func TestFormatting(t *testing.T) {
	for in, want := range map[float64]string{0: "0 B", 1023: "1023 B", 18 << 20: "18 MB", 342 << 20: "342 MB", 1.8 * (1 << 30): "1.8 GB", 4 << 30: "4 GB"} {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%v) = %q, want %q", in, got, want)
		}
	}
	if got := sparkline([]float64{0, 50, 100}, 10); got != "▁▄█" {
		t.Errorf("sparkline = %q", got)
	}
	for _, w := range []int{40, 77, 120} {
		total := 0
		widths := columnWidths(newModel(nil).columns(), w)
		for _, cw := range widths {
			if cw > 0 {
				total += cw + 2
			}
		}
		if total != w {
			t.Errorf("columns at %d use %d cells", w, total)
		}
	}
}

func loadDetail(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	for _, msg := range drain(cmd) {
		if _, ok := msg.(detailMsg); ok {
			m, _ = step(t, m, msg)
		}
	}
	return m
}

func TestZoomTogglesFullWidthDetails(t *testing.T) {
	m, cmd := step(t, newModel(nil), tea.WindowSizeMsg{Width: 160, Height: 30}, sample())
	m = loadDetail(t, m, cmd)
	assertContains(t, screen(m), "RESOURCES", "NAME", "z full width")

	m, _ = step(t, m, press("z"))
	s := screen(m)
	if strings.Contains(s, "RESOURCES") || strings.Contains(s, "STATUS") {
		t.Error("full-width details must hide the sidebar and the list")
	}
	assertContains(t, s, "web-nginx   RUNNING", "z back", "DETAILS")
	assertSize(t, m, 160, 30)
	if got := m.layout(); !got.detailOnly || got.hasSidebar {
		t.Errorf("layout = %+v, want details only", got)
	}

	// moving still changes the container shown
	m, _ = step(t, m, press("down"))
	assertContains(t, screen(m), "api-server   RUNNING")

	m, _ = step(t, m, press("z"))
	assertContains(t, screen(m), "RESOURCES", "NAME")
	assertSize(t, m, 160, 30)

	m, _ = step(t, m, press("z"), press("esc"))
	if m.zoom {
		t.Error("esc must leave full width")
	}
}

func TestRevealSecrets(t *testing.T) {
	m, cmd := step(t, newModel(nil), tea.WindowSizeMsg{Width: 160, Height: 30}, sample())
	m = loadDetail(t, m, cmd)
	m, _ = step(t, m, press("tab"), press("tab"), press("tab")) // Env
	s := screen(m)
	assertContains(t, s, "DB_PASSWORD=••••••••", "NODE_ENV=production", "1 secret value(s) hidden · v to show")
	if strings.Contains(s, "hunter2") {
		t.Fatal("secret shown before reveal")
	}

	m, _ = step(t, m, press("v"))
	assertContains(t, screen(m), "DB_PASSWORD=hunter2", "visible · v to hide")

	m, _ = step(t, m, press("i")) // inspect follows the same toggle
	assertContains(t, screen(m), `"DB_PASSWORD=hunter2"`)
	m, _ = step(t, m, press("v"))
	assertContains(t, screen(m), `"DB_PASSWORD=••••••••"`, `"NODE_ENV=production"`)

	// revealing is per container: moving away hides again
	m, _ = step(t, m, press("v"), press("down"))
	if m.reveal {
		t.Error("reveal must reset when another container is selected")
	}
}

func TestMaskEnvJSON(t *testing.T) {
	for in, want := range map[string]string{
		`    "API_TOKEN=abc",`:  `    "API_TOKEN=••••••••",`,
		`    "DB_PASSWORD=x=y"`: `    "DB_PASSWORD=••••••••"`,
		`    "PATH=/usr/bin",`:  `    "PATH=/usr/bin",`,
		`  "Password": "nope",`: `  "Password": "nope",`,
	} {
		if got := maskEnvJSON(in); got != want {
			t.Errorf("maskEnvJSON(%q) = %q, want %q", in, got, want)
		}
	}
}
