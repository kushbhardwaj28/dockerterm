package ui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/kushbhardwaj28/dockerterm/internal/docker"
)

type fakeBackend struct {
	containers []docker.Container
	stats      map[string]docker.Stats
	listErr    error
	statsErr   error
	version    string
	statsIDs   []string
}

func (f *fakeBackend) ListContainers(context.Context) ([]docker.Container, error) {
	return f.containers, f.listErr
}

func (f *fakeBackend) Stats(_ context.Context, ids []string) (map[string]docker.Stats, error) {
	f.statsIDs = ids
	return f.stats, f.statsErr
}

func (f *fakeBackend) ServerVersion(context.Context) (string, error) { return f.version, nil }

func newTestModel() Model { return New(&fakeBackend{}, Options{Endpoint: "default socket"}) }

func update(t *testing.T, m tea.Model, msgs ...tea.Msg) Model {
	t.Helper()
	for _, msg := range msgs {
		m, _ = m.Update(msg)
	}
	return m.(Model)
}

// content is the rendered screen without ANSI styling.
func content(m Model) string { return ansi.Strip(m.View().Content) }

func assertSize(t *testing.T, m Model, w, h int) {
	t.Helper()
	c := content(m)
	if gw, gh := lipgloss.Width(c), lipgloss.Height(c); gw != w || gh != h {
		t.Errorf("view size = %dx%d, want %dx%d", gw, gh, w, h)
	}
}

func running(id, name string) docker.Container {
	return docker.Container{ID: id, Name: name, State: "running", Status: "Up 3 days"}
}

func TestRefreshCmdOnlyAsksStatsForRunningContainers(t *testing.T) {
	backend := &fakeBackend{containers: []docker.Container{
		running("aaa", "web"),
		{ID: "bbb", Name: "old", State: "exited"},
	}}
	msg := New(backend, Options{}).refreshCmd()().(refreshMsg)
	if len(msg.containers) != 2 || strings.Join(backend.statsIDs, ",") != "aaa" {
		t.Errorf("stats asked for %v, want only the running container", backend.statsIDs)
	}
}

func TestArrowKeysMoveContainerSelection(t *testing.T) {
	m := update(t, newTestModel(),
		refreshMsg{containers: []docker.Container{running("1", "web"), running("2", "api")}},
		tea.KeyPressMsg{Code: tea.KeyDown},
	)
	if c := m.selectedContainer(); c == nil || c.Name != "api" {
		t.Fatalf("selected container = %v, want api", c)
	}
}

func TestWideLayoutRendersSelectedContainerAndFooter(t *testing.T) {
	m := update(t, newTestModel(),
		tea.WindowSizeMsg{Width: 120, Height: 40},
		refreshMsg{containers: []docker.Container{{
			Name: "api-server", Image: "myorg/api:2.4.1", ID: "a81be44c09f2deadbeef", State: "running", Status: "Up 3 days",
			Ports: []docker.Port{{IP: "0.0.0.0", PrivatePort: 8080, PublicPort: 8080, Type: "tcp"}},
		}}, stats: map[string]docker.Stats{"a81be44c09f2deadbeef": {CPUPercent: 3.4, MemoryUsage: 34 << 20, MemoryLimit: 1 << 30}}},
		versionMsg{version: "28.5.1"},
		tea.KeyPressMsg{Code: tea.KeyEnter},
	)
	c := content(m)
	for _, want := range []string{"dockerterm", "default socket", "28.5.1", "RESOURCES", "Containers 1", "Images",
		"api-server", "Info", "a81be44c09f2", "Stats", "3.40%", "34MiB", "enter details", "q quit"} {
		if !strings.Contains(c, want) {
			t.Errorf("view missing %q", want)
		}
	}
	assertSize(t, m, 120, 40)
}

func TestEnterTogglesDetailsForCurrentSelection(t *testing.T) {
	m := update(t, newTestModel(),
		tea.WindowSizeMsg{Width: 60, Height: 20},
		refreshMsg{containers: []docker.Container{running("first-id", "web"), running("second-id", "api")}},
		tea.KeyPressMsg{Code: tea.KeyDown},
		tea.KeyPressMsg{Code: tea.KeyEnter},
	)
	if c := content(m); !strings.Contains(c, "second-id") || strings.Contains(c, "first-id") {
		t.Errorf("expanded details should show the selected api container")
	}
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if c := content(m); strings.Contains(c, "second-id") || !strings.Contains(c, "Name") {
		t.Errorf("collapsed view should show the table without details")
	}
	assertSize(t, m, 60, 20)
}

func TestEmptyResponseClearsDetailsWithoutChangingLayoutSize(t *testing.T) {
	m := update(t, newTestModel(),
		tea.WindowSizeMsg{Width: 120, Height: 40},
		refreshMsg{containers: []docker.Container{running("old-id", "api")}},
		tea.KeyPressMsg{Code: tea.KeyEnter},
		refreshMsg{},
	)
	c := content(m)
	if !strings.Contains(c, "Containers 0") || !strings.Contains(c, "No containers") || strings.Contains(c, "old-id") {
		t.Errorf("empty response must clear the previous selection")
	}
	if m.detailsOpen {
		t.Error("details must close when there is nothing to show")
	}
	assertSize(t, m, 120, 40)
}

func TestResizingOpenDetailsPreservesSelection(t *testing.T) {
	m := update(t, newTestModel(),
		tea.WindowSizeMsg{Width: 120, Height: 40},
		refreshMsg{containers: []docker.Container{running("first-id", "web"), running("second-id", "api")}},
		tea.KeyPressMsg{Code: tea.KeyDown},
		tea.KeyPressMsg{Code: tea.KeyEnter},
		tea.WindowSizeMsg{Width: 80, Height: 24},
	)
	if c := content(m); !strings.Contains(c, "second-id") || strings.Contains(c, "first-id") {
		t.Errorf("resized details must keep the selected container")
	}
	assertSize(t, m, 80, 24)
}

func TestRefreshPreservesSelectionByIDAndReplacesStats(t *testing.T) {
	m := update(t, newTestModel(), refreshMsg{
		containers: []docker.Container{running("aaa", "web"), running("bbb", "api")},
		stats:      map[string]docker.Stats{"bbb": {CPUPercent: 3.4, MemoryUsage: 34 << 20}},
	})
	m.table.SetCursor(1)
	next, cmd := m.Update(refreshMsg{
		containers: []docker.Container{running("bbb", "api"), running("aaa", "web")},
		stats:      map[string]docker.Stats{"bbb": {CPUPercent: 5, MemoryUsage: 40 << 20}},
	})
	m = next.(Model)
	if cmd == nil || m.table.Cursor() != 0 || m.containers[0].ID != "bbb" {
		t.Errorf("refresh lost selected container after reorder: cursor = %d", m.table.Cursor())
	}
	if row := m.table.Rows()[0]; row[colCPU] != "5.00%" || row[colMem] != "40MiB" {
		t.Errorf("selected row not updated with current stats: %v", row)
	}
}

func TestRefreshFailureKeepsListButClearsStats(t *testing.T) {
	m := update(t, newTestModel(),
		refreshMsg{containers: []docker.Container{running("aaa", "web")}, stats: map[string]docker.Stats{"aaa": {CPUPercent: 3.4}}},
	)
	next, cmd := m.Update(refreshMsg{listErr: errors.New("daemon unavailable")})
	m = next.(Model)
	if cmd == nil || len(m.containers) != 1 || len(m.stats) != 0 || m.refreshErr == nil {
		t.Errorf("error refresh must retain containers, clear metrics and schedule retry")
	}
	if c := content(m); !strings.Contains(c, "Data stale") || !strings.Contains(c, "daemon unavailable") {
		t.Error("refresh error must show stale state and the reason")
	}
	if row := m.table.Rows()[0]; row[colCPU] != "-" {
		t.Errorf("stale row still shows metrics: %v", row)
	}
}

func TestInitialDockerFailureAndPartialStatsFailure(t *testing.T) {
	m := update(t, newTestModel(), refreshMsg{listErr: errors.New("no daemon")})
	if c := content(m); !strings.Contains(c, "Docker unavailable") || strings.Contains(c, "Loading containers") {
		t.Error("initial error must replace the loading message")
	}
	m = update(t, m, refreshMsg{
		containers: []docker.Container{running("aaa", "web"), running("bbb", "api")},
		stats:      map[string]docker.Stats{"aaa": {CPUPercent: 1}},
		statsErr:   errors.New("stats bbb: timeout"),
	})
	if c := content(m); !strings.Contains(c, "Stats unavailable") {
		t.Error("stats failure must be visible while rows remain")
	}
	if rows := m.table.Rows(); rows[0][colCPU] != "1.00%" || rows[1][colCPU] != "-" {
		t.Errorf("partial stats must still be shown: %v", rows)
	}
}

func TestStaleTicksAreIgnored(t *testing.T) {
	m := update(t, newTestModel(), refreshMsg{})
	gen := m.tickGen
	next, cmd := m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	m = next.(Model)
	if cmd == nil || !m.refreshing {
		t.Fatal("ctrl+r must start a refresh")
	}
	if _, cmd := m.Update(tickMsg{gen: gen}); cmd != nil {
		t.Error("a tick scheduled before ctrl+r must not start a second refresh loop")
	}
}

func TestVersionIsRequestedAfterFirstSuccessfulRefresh(t *testing.T) {
	m := update(t, newTestModel(), refreshMsg{listErr: errors.New("down")})
	if m.versionAsked {
		t.Error("version must not be requested while the daemon is down")
	}
	m = update(t, m, refreshMsg{containers: []docker.Container{running("a", "web")}})
	if !m.versionAsked {
		t.Error("version must be requested once the daemon answers")
	}
	m = update(t, m, versionMsg{version: ""})
	if m.versionAsked {
		t.Error("a failed version request must be retried")
	}
}

func TestQuitKey(t *testing.T) {
	_, cmd := newTestModel().Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if cmd == nil {
		t.Fatal("q must quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("q must return tea.Quit")
	}
}
