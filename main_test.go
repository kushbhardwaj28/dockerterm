package main

import (
	"errors"
	"strings"
	"testing"

	"docker_cmd"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func TestArrowKeysMoveContainerSelection(t *testing.T) {
	model := NewModel()
	updated, _ := model.Update(refreshResult{containers: []docker_cmd.DockerPs{
		{Names: "web", Image: "nginx"},
		{Names: "api", Image: "myorg/api"},
	}})
	updated, _ = updated.Update(tea.KeyPressMsg{Code: tea.KeyDown})

	selected := updated.(Model).dockerPsTable.SelectedRow()
	if len(selected) == 0 || selected[0] != "api" {
		t.Fatalf("selected container = %v, want api", selected)
	}
}

func TestWideLayoutRendersSelectedContainerAndFooter(t *testing.T) {
	model := NewModel()
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	updated, _ = updated.Update(refreshResult{containers: []docker_cmd.DockerPs{
		{Names: "api-server", Image: "myorg/api:2.4.1", ID: "a81be4", Status: "Up 3 days", Ports: "0.0.0.0:8080->8080/tcp"},
	}})
	updated, _ = updated.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	content := updated.View().Content
	for _, want := range []string{"DockerTerm", "RESOURCES", "Containers 1", "Images", "api-server", "Info", "a81be4", "enter details", "q quit"} {
		if !strings.Contains(content, want) {
			t.Errorf("view missing %q", want)
		}
	}
	if width, height := lipgloss.Width(content), lipgloss.Height(content); width != 120 || height != 40 {
		t.Errorf("view size = %dx%d, want 120x40", width, height)
	}
}

func TestEnterTogglesDetailsForCurrentSelection(t *testing.T) {
	model := NewModel()
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	updated, _ = updated.Update(refreshResult{containers: []docker_cmd.DockerPs{
		{Names: "web", ID: "first-id", Image: "nginx"},
		{Names: "api", ID: "second-id", Image: "myorg/api"},
	}})
	updated, _ = updated.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	updated, _ = updated.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	if content := updated.View().Content; !strings.Contains(content, "second-id") || strings.Contains(content, "first-id") {
		t.Errorf("expanded details should show the selected api container")
	}
	updated, _ = updated.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	content := updated.View().Content
	if strings.Contains(content, "second-id") || !strings.Contains(content, "Name") {
		t.Errorf("collapsed view should show the table without details")
	}
	if width, height := lipgloss.Width(content), lipgloss.Height(content); width != 60 || height != 20 {
		t.Errorf("view size = %dx%d, want 60x20", width, height)
	}
}

func TestEmptyResponseClearsDetailsWithoutChangingLayoutSize(t *testing.T) {
	model := NewModel()
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	updated, _ = updated.Update(refreshResult{containers: []docker_cmd.DockerPs{{Names: "api", ID: "old-id"}}})
	updated, _ = updated.Update(refreshResult{})

	content := updated.View().Content
	if !strings.Contains(content, "Containers 0") || !strings.Contains(content, "No running containers") || strings.Contains(content, "old-id") {
		t.Errorf("empty response must clear the previous selection")
	}
	if width, height := lipgloss.Width(content), lipgloss.Height(content); width != 120 || height != 40 {
		t.Errorf("empty view size = %dx%d, want 120x40", width, height)
	}
}

func TestResizingOpenDetailsPreservesSelection(t *testing.T) {
	model := NewModel()
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	updated, _ = updated.Update(refreshResult{containers: []docker_cmd.DockerPs{
		{Names: "web", ID: "first-id"},
		{Names: "api", ID: "second-id"},
	}})
	updated, _ = updated.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	updated, _ = updated.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	updated, _ = updated.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	content := updated.View().Content
	if !strings.Contains(content, "second-id") || strings.Contains(content, "first-id") {
		t.Errorf("resized details must keep the selected container")
	}
	if width, height := lipgloss.Width(content), lipgloss.Height(content); width != 80 || height != 24 {
		t.Errorf("resized view size = %dx%d, want 80x24", width, height)
	}
}

func TestRefreshPreservesSelectionByIDAndReplacesStats(t *testing.T) {
	model := NewModel()
	updated, next := model.Update(refreshResult{
		containers: []docker_cmd.DockerPs{{ID: "aaa", Names: "web"}, {ID: "bbb", Names: "api"}},
		stats:      []docker_cmd.DockerStats{{ID: "bbb", CPUPerc: "3.40%", MemUsage: "34MiB / 1GiB"}},
	})
	if next == nil {
		t.Fatal("refresh must schedule the next cycle")
	}
	selected := updated.(Model)
	selected.dockerPsTable.SetCursor(1)
	updated, next = selected.Update(refreshResult{
		containers: []docker_cmd.DockerPs{{ID: "bbb", Names: "api"}, {ID: "aaa", Names: "web"}},
		stats:      []docker_cmd.DockerStats{{ID: "bbb", CPUPerc: "5.00%", MemUsage: "40MiB / 1GiB"}},
	})
	result := updated.(Model)
	if next == nil || result.dockerPsTable.Cursor() != 0 || result.commandResult[0].ID != "bbb" {
		t.Errorf("refresh lost selected container after reorder: cursor = %d", result.dockerPsTable.Cursor())
	}
	if result.statsByID["bbb"].CPUPerc != "5.00%" {
		t.Errorf("stale stats after refresh: %+v", result.statsByID["bbb"])
	}
	if row := result.dockerPsTable.Rows()[0]; row[4] != "5.00%" || row[5] != "40MiB" {
		t.Errorf("selected table row not updated with current stats: %v", row)
	}
}

func TestRefreshFailureKeepsListButClearsStats(t *testing.T) {
	model := NewModel()
	updated, _ := model.Update(refreshResult{
		containers: []docker_cmd.DockerPs{{ID: "aaa", Names: "web"}},
		stats:      []docker_cmd.DockerStats{{ID: "aaa", CPUPerc: "3.40%"}},
	})
	updated, next := updated.Update(refreshResult{listErr: errors.New("daemon unavailable")})
	result := updated.(Model)
	if next == nil || len(result.commandResult) != 1 || len(result.statsByID) != 0 || result.refreshErr == nil {
		t.Errorf("error refresh must retain containers, clear metrics and schedule retry")
	}
	if content := result.View().Content; !strings.Contains(content, "Data stale") || !strings.Contains(content, "-") {
		t.Error("refresh error must show stale state with unavailable metrics")
	}
}

func TestInitialDockerFailureAndStatsFailureShowRetryState(t *testing.T) {
	model := NewModel()
	failed, _ := model.Update(refreshResult{listErr: errors.New("no daemon")})
	if content := failed.View().Content; !strings.Contains(content, "Docker unavailable") || strings.Contains(content, "Loading containers") {
		t.Error("initial command error must replace the loading message")
	}
	updated, _ := failed.Update(refreshResult{
		containers: []docker_cmd.DockerPs{{ID: "aaa", Names: "web"}},
		statsErr:   errors.New("stats timed out"),
	})
	if content := updated.View().Content; !strings.Contains(content, "Stats unavailable") {
		t.Error("stats failure must be visible while container rows remain available")
	}
}
