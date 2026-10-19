package ui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/docker/go-units"

	"github.com/kushbhardwaj28/dockerterm/internal/docker"
)

// layoutSize is the space each region gets for the current window.
type layoutSize struct {
	width, height, bodyHeight             int
	sidebarWidth, tableWidth, detailWidth int
	showDetails, detailOnly               bool
}

const (
	headerLines = 1
	footerLines = 2 // status line + key help
	minWidth    = 80
	minHeight   = 24
)

func (m Model) layoutSize() layoutSize {
	width, height := m.width, m.height
	if width <= 0 {
		width = minWidth
	}
	if height <= 0 {
		height = minHeight
	}
	size := layoutSize{width: width, height: height, bodyHeight: max(1, height-headerLines-footerLines)}
	if width >= 80 {
		size.sidebarWidth = min(24, max(18, width/6))
	}
	size.showDetails = m.detailsOpen && len(m.containers) > 0
	if size.showDetails {
		if width < 100 {
			size.detailOnly = true // not enough room side by side
		} else {
			size.detailWidth = max(30, width/3)
		}
	}
	size.tableWidth = width
	if size.sidebarWidth > 0 {
		size.tableWidth -= size.sidebarWidth + 1
	}
	if size.detailWidth > 0 {
		size.tableWidth -= size.detailWidth + 1
	}
	return size
}

func (m Model) render() string {
	size := m.layoutSize()
	var body string
	if size.detailOnly {
		body = m.renderDetails(size.width, size.bodyHeight)
	} else {
		body = m.renderList(size.tableWidth, size.bodyHeight)
		if size.sidebarWidth > 0 {
			body = lipgloss.JoinHorizontal(lipgloss.Top, m.renderSidebar(size.sidebarWidth, size.bodyHeight), separator(size.bodyHeight), body)
		}
		if size.detailWidth > 0 {
			body = lipgloss.JoinHorizontal(lipgloss.Top, body, separator(size.bodyHeight), m.renderDetails(size.detailWidth, size.bodyHeight))
		}
	}
	return lipgloss.JoinVertical(lipgloss.Left,
		m.renderHeader(size.width),
		body,
		m.renderStatus(size.width),
		m.renderHelp(size.width),
	)
}

func (m Model) renderHeader(width int) string {
	var running, paused, stopped int
	for _, c := range m.containers {
		switch c.State {
		case "running":
			running++
		case "paused":
			paused++
		default:
			stopped++
		}
	}
	parts := []string{accentStyle.Bold(true).Render("dockerterm")}
	if m.opts.Endpoint != "" {
		parts = append(parts, mutedStyle.Render("ctx ")+m.opts.Endpoint)
	}
	if m.engineVersion != "" {
		parts = append(parts, mutedStyle.Render("engine ")+m.engineVersion)
	}
	parts = append(parts,
		mutedStyle.Render("running ")+runningStyle.Bold(true).Render(fmt.Sprint(running)),
		mutedStyle.Render("paused ")+warnStyle.Bold(true).Render(fmt.Sprint(paused)),
		mutedStyle.Render("stopped ")+stoppedStyle.Bold(true).Render(fmt.Sprint(stopped)),
	)
	return line(" "+strings.Join(parts, "   "), width, barStyle)
}

func (m Model) renderSidebar(width, height int) string {
	items := []string{
		line(" RESOURCES", width, mutedStyle),
		line(fmt.Sprintf(" 1 Containers %d", len(m.containers)), width, accentStyle.Bold(true)),
		line(" 2 Images", width, mutedStyle),
		line(" 3 Volumes", width, mutedStyle),
		line(" 4 Networks", width, mutedStyle),
	}
	return panel(strings.Join(items, "\n"), width, height)
}

func (m Model) renderList(width, height int) string {
	switch {
	case !m.loaded && m.refreshErr != nil:
		return panel(line(" Docker unavailable - retrying", width, warnStyle), width, height)
	case !m.loaded:
		return panel(line(" Loading containers...", width, mutedStyle), width, height)
	case len(m.containers) == 0:
		return panel(line(" No containers", width, mutedStyle), width, height)
	}
	return panel(m.table.View(), width, height)
}

func (m Model) renderDetails(width, height int) string {
	c := m.selectedContainer()
	if c == nil {
		return panel(" No container selected", width, height)
	}
	lines := []string{
		line(" "+c.Name, width, accentStyle.Bold(true)),
		line(" Info", width, barStyle),
	}
	field := func(label, value string) {
		if value != "" {
			lines = append(lines, line(fmt.Sprintf(" %-9s %s", label, value), width, mutedStyle))
		}
	}
	field("ID", c.ShortID())
	field("Status", c.Status)
	field("Health", c.Health)
	field("Image", c.Image)
	field("Command", c.Command)
	if !c.Created.IsZero() {
		field("Created", units.HumanDuration(time.Since(c.Created))+" ago")
	}
	field("Ports", docker.FormatPorts(c.Ports))
	field("Networks", strings.Join(c.Networks, ", "))
	field("Compose", c.ComposeProject())

	if s, ok := m.stats[c.ID]; ok {
		lines = append(lines, "", line(" Stats", width, barStyle))
		field("CPU", fmt.Sprintf("%.2f%%", s.CPUPercent))
		mem := units.BytesSize(float64(s.MemoryUsage))
		if s.MemoryLimit > 0 {
			mem += fmt.Sprintf(" / %s (%.1f%%)", units.BytesSize(float64(s.MemoryLimit)), s.MemoryPercent())
		}
		field("Memory", mem)
		field("PIDs", fmt.Sprint(s.PIDs))
		field("Net I/O", units.BytesSize(float64(s.NetRx))+" / "+units.BytesSize(float64(s.NetTx)))
	}
	return panel(strings.Join(lines, "\n"), width, height)
}

func (m Model) renderStatus(width int) string {
	text := fmt.Sprintf("CONTAINERS  |  %d listed", len(m.containers))
	style := barStyle
	if m.refreshErr != nil {
		style = barStyle.Foreground(colorWarn)
		switch {
		case !m.loaded:
			text = "Docker unavailable - retrying: " + m.refreshErr.Error()
		case m.statsFailed:
			text = "Stats unavailable - retrying: " + m.refreshErr.Error()
		default:
			text = "Data stale - retrying: " + m.refreshErr.Error()
		}
	}
	return line(" "+oneLine(text), width, style)
}

func (m Model) renderHelp(width int) string {
	parts := make([]string, 0, 4)
	for _, b := range m.keys.shortHelp() {
		h := b.Help()
		parts = append(parts, keyStyle.Render(h.Key)+" "+mutedStyle.Render(h.Desc))
	}
	return line(" "+strings.Join(parts, "   "), width, lipgloss.NewStyle())
}

// line renders text on exactly one row of the given width.
func line(text string, width int, style lipgloss.Style) string {
	return style.Width(width).MaxWidth(width).Render(ansi.Truncate(text, width, "…"))
}

// panel pads or clips content to a fixed box.
func panel(content string, width, height int) string {
	return lipgloss.NewStyle().Width(width).Height(height).MaxWidth(width).MaxHeight(height).Render(content)
}

func separator(height int) string {
	return mutedStyle.Render(strings.TrimSuffix(strings.Repeat("│\n", height), "\n"))
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
