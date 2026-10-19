package ui

import (
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/kushbhardwaj28/dockerterm/internal/docker"
)

func TestTableUsesContainerFieldsAndFitsAvailableWidth(t *testing.T) {
	tbl := newContainerTable()
	tbl.SetRows(containerRows([]docker.Container{{
		Name: "api", Image: "myorg/api", State: "running", Status: "Up 3 days",
		Ports: []docker.Port{{PrivatePort: 8080, Type: "tcp"}},
	}}, nil))
	resizeTable(&tbl, 95, 12)

	want := map[int]string{colName: "api", colImage: "myorg/api", colStatus: "● Up 3 days", colPorts: "8080/tcp", colCPU: "-", colMem: "-"}
	for i, v := range want {
		if got := tbl.Rows()[0][i]; got != v {
			t.Errorf("column %s = %q, want %q", columnTitles[i], got, v)
		}
	}
	for _, width := range []int{48, 70, 95, 140} {
		resizeTable(&tbl, width, 12)
		used := 0
		for _, c := range tbl.Columns() {
			if c.Width > 0 {
				used += c.Width + 2
			}
		}
		if used != width {
			t.Errorf("width %d: columns use %d cells", width, used)
		}
		if got := lipgloss.Width(tbl.View()); got > width {
			t.Errorf("width %d: table renders at %d cells", width, got)
		}
	}
}

func TestNarrowTableHidesImageAndPortsButKeepsMetrics(t *testing.T) {
	cols := columnsForWidth(48)
	if cols[colImage].Width != 0 || cols[colPorts].Width != 0 {
		t.Errorf("narrow columns = %v, want image and ports hidden", cols)
	}
	for _, i := range []int{colName, colStatus, colCPU, colMem} {
		if cols[i].Width == 0 {
			t.Errorf("narrow columns = %v, want %s visible", cols, columnTitles[i])
		}
	}
}

func TestStateGlyphs(t *testing.T) {
	for state, want := range map[string]string{"running": "●", "paused": "‖", "restarting": "↻", "exited": "■", "created": "■"} {
		if got := stateGlyph(state); got != want {
			t.Errorf("stateGlyph(%q) = %q, want %q", state, got, want)
		}
	}
}
