package ui_components

import (
	"testing"

	"docker_cmd"

	"charm.land/lipgloss/v2"
)

func TestTableUsesContainerFieldsAndFitsAvailableWidth(t *testing.T) {
	table := CreateDockerPSTable([]docker_cmd.DockerPs{
		{Names: "api", Image: "myorg/api", Status: "Up 3 days", Ports: "8080/tcp"},
	})
	UpdateTableDimensions(&table, 48, 12)

	if len(table.Rows()) != 1 {
		t.Fatalf("rows = %d, want 1", len(table.Rows()))
	}
	want := []string{"api", "myorg/api", "Up 3 days", "8080/tcp"}
	for index, value := range want {
		if table.Rows()[0][index] != value {
			t.Errorf("column %d = %q, want %q", index, table.Rows()[0][index], value)
		}
	}
	used := 0
	for _, column := range table.Columns() {
		if column.Width > 0 {
			used += column.Width + 2
		}
	}
	if used != 48 {
		t.Errorf("rendered column width = %d, want 48", used)
	}
	if renderedWidth := lipgloss.Width(table.View()); renderedWidth > 48 {
		t.Errorf("table renders at %d cells, exceeding available width 48", renderedWidth)
	}
}

func TestTableMergesStatsByIDAndKeepsMetricsAtNarrowWidths(t *testing.T) {
	containers := []docker_cmd.DockerPs{
		{ID: "bbb", Names: "api", Image: "myorg/api", Status: "Up 3 days", Ports: "8080/tcp"},
		{ID: "aaa", Names: "old", Status: "Exited"},
	}
	stats := map[string]docker_cmd.DockerStats{
		"bbb": {ID: "bbb", CPUPerc: "3.40%", MemUsage: "34MiB / 1GiB"},
	}
	table := CreateDockerPSTable(containers)
	UpdateTableDimensions(&table, 95, 12)
	SetTableData(&table, containers, stats)
	if got := table.Rows()[0]; len(got) != 6 || got[4] != "3.40%" || got[5] != "34MiB" {
		t.Errorf("wide row = %v, want CPU and used memory", got)
	}
	if got := table.Rows()[1]; got[4] != "-" || got[5] != "-" {
		t.Errorf("stopped row = %v, want unavailable metrics", got)
	}
	UpdateTableDimensions(&table, 48, 12)
	SetTableData(&table, containers, stats)
	if got := table.Rows()[0]; len(got) != 6 || got[0] != "api" || got[4] != "3.40%" || got[5] != "34MiB" {
		t.Errorf("narrow row = %v, want name, CPU and memory", got)
	}
	if columns := table.Columns(); columns[1].Width != 0 || columns[3].Width != 0 || columns[2].Width == 0 || columns[4].Width == 0 || columns[5].Width == 0 {
		t.Errorf("narrow columns = %v, want image/ports hidden and status/metrics visible", columns)
	}
	if width := lipgloss.Width(table.View()); width > 48 {
		t.Errorf("narrow table width = %d, want at most 48", width)
	}
}
