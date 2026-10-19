package ui

import (
	"fmt"

	"charm.land/bubbles/v2/table"
	"charm.land/lipgloss/v2"
	"github.com/docker/go-units"

	"github.com/kushbhardwaj28/dockerterm/internal/docker"
)

// Column indexes in a container row.
const (
	colName = iota
	colImage
	colStatus
	colPorts
	colCPU
	colMem
	numCols
)

var columnTitles = [numCols]string{"Name", "Image", "Status", "Ports", "CPU", "Mem"}

func newContainerTable() table.Model {
	t := table.New(
		table.WithColumns(columnsForWidth(80)),
		table.WithFocused(true),
		table.WithKeyMap(tableKeyMap()),
	)
	s := table.DefaultStyles()
	s.Header = s.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(colorBorder).
		BorderBottom(true).
		Bold(false)
	s.Selected = s.Selected.
		Foreground(colorSelectedFg).
		Background(colorSelectedBg).
		Bold(false)
	t.SetStyles(s)
	return t
}

// resizeTable fits the columns into width cells and the rows into height lines.
func resizeTable(t *table.Model, width, height int) {
	t.SetColumns(columnsForWidth(width))
	t.SetWidth(max(0, width))
	t.SetHeight(max(2, height))
}

// columnsForWidth shares width between the columns. Each visible column
// costs two cells of padding; Image and Ports are dropped on narrow screens.
func columnsForWidth(width int) []table.Column {
	visible := numCols
	if width < 90 {
		visible-- // ports
	}
	if width < 65 {
		visible-- // image
	}
	usable := max(0, width-visible*2)

	widths := [numCols]int{}
	widths[colName] = usable * 24 / 100
	widths[colStatus] = usable * 22 / 100
	widths[colCPU] = usable * 14 / 100
	if width >= 65 {
		widths[colImage] = usable * 20 / 100
	}
	if width >= 90 {
		widths[colPorts] = usable * 10 / 100
	}
	widths[colMem] = usable - widths[colName] - widths[colImage] - widths[colStatus] - widths[colPorts] - widths[colCPU]

	cols := make([]table.Column, numCols)
	for i := range cols {
		cols[i] = table.Column{Title: columnTitles[i], Width: widths[i]}
	}
	return cols
}

func containerRows(containers []docker.Container, stats map[string]docker.Stats) []table.Row {
	rows := make([]table.Row, 0, len(containers))
	for _, c := range containers {
		row := make(table.Row, numCols)
		row[colName] = orDash(c.Name)
		row[colImage] = orDash(c.Image)
		row[colStatus] = stateGlyph(c.State) + " " + orDash(c.Status)
		row[colPorts] = orDash(docker.FormatPorts(c.Ports))
		row[colCPU], row[colMem] = "-", "-"
		if s, ok := stats[c.ID]; ok {
			row[colCPU] = fmt.Sprintf("%.2f%%", s.CPUPercent)
			row[colMem] = units.BytesSize(float64(s.MemoryUsage))
		}
		rows = append(rows, row)
	}
	return rows
}

func stateGlyph(state string) string {
	switch state {
	case "running":
		return "●"
	case "paused":
		return "‖"
	case "restarting":
		return "↻"
	default:
		return "■"
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
