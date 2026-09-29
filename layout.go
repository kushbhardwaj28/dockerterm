package main

import (
	"fmt"
	"strings"

	"docker_cmd"
	"ui_components"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

var (
	mutedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	accentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("117"))
	greenStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("114"))
	barStyle    = lipgloss.NewStyle().Background(lipgloss.Color("235"))
)

type layoutSize struct {
	width, height, bodyHeight, sidebarWidth, tableWidth, detailWidth int
	showDetails, detailOnly                                          bool
}

func (m Model) layoutSize() layoutSize {
	width, height := m.width, m.height
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 24
	}
	size := layoutSize{width: width, height: height, bodyHeight: max(1, height-3)}
	if width >= 80 {
		size.sidebarWidth = min(24, max(18, width/6))
	}
	size.showDetails = m.detailsOpen && len(m.commandResult) > 0
	if size.showDetails {
		if width < 100 {
			size.detailOnly = true
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

func (m *Model) resizeTable() {
	size := m.layoutSize()
	ui_components.UpdateTableDimensions(&m.dockerPsTable, size.tableWidth, max(2, size.bodyHeight-1))
}

func line(text string, width int, style lipgloss.Style) string {
	return style.Width(width).MaxWidth(width).Render(ansi.Truncate(text, width, "…"))
}

func panel(content string, width, height int) string {
	return lipgloss.NewStyle().Width(width).Height(height).MaxWidth(width).MaxHeight(height).Render(content)
}

func separator(height int) string {
	return strings.TrimSuffix(strings.Repeat("|\n", height), "\n")
}

func (m Model) selectedContainer() *docker_cmd.DockerPs {
	index := m.dockerPsTable.Cursor()
	if index < 0 || index >= len(m.commandResult) {
		return nil
	}
	return &m.commandResult[index]
}

func (m Model) renderDetails(width, height int) string {
	container := m.selectedContainer()
	if container == nil {
		return panel(" No container selected", width, height)
	}
	fields := []struct{ label, value string }{
		{"ID", container.ID},
		{"Status", container.Status},
		{"Image", container.Image},
		{"Command", container.Command},
		{"Created", container.CreatedAt},
		{"Ports", container.Ports},
		{"Networks", container.Networks},
	}
	lines := []string{
		line(" "+container.Names, width, accentStyle.Bold(true)),
		line(" Info", width, barStyle),
	}
	for _, field := range fields {
		if field.value != "" {
			lines = append(lines, line(fmt.Sprintf(" %-9s %s", field.label, field.value), width, mutedStyle))
		}
	}
	return panel(strings.Join(lines, "\n"), width, height)
}

func (m Model) renderTable(width, height int) string {
	// heading := line(fmt.Sprintf(" Containers [%d]", len(m.commandResult)), width, barStyle.Bold(true))
	if !m.loaded {
		if m.refreshErr != nil {
			return panel(line(" Docker unavailable - retrying", width, mutedStyle), width, height)
		}
		return panel(line(" Loading containers...", width, mutedStyle), width, height)
	}
	if len(m.commandResult) == 0 {
		return panel(line(" No running containers", width, mutedStyle), width, height)
	}
	return panel(m.dockerPsTable.View(), width, height)
}

func (m Model) renderSidebar(width, height int) string {
	items := []string{
		line(" RESOURCES", width, mutedStyle),
		line(fmt.Sprintf(" 1 Containers %d", len(m.commandResult)), width, accentStyle.Bold(true)),
		line(" 2 Images", width, mutedStyle),
		line(" 3 Volumes", width, mutedStyle),
		line(" 4 Networks", width, mutedStyle),
	}
	return panel(strings.Join(items, "\n"), width, height)
}

func (m Model) renderLayout() string {
	size := m.layoutSize()
	header := line(fmt.Sprintf(" %s   containers %s", m.title, greenStyle.Render(fmt.Sprint(len(m.commandResult)))), size.width, barStyle.Bold(true))
	var body string
	if size.detailOnly {
		body = m.renderDetails(size.width, size.bodyHeight)
	} else {
		body = m.renderTable(size.tableWidth, size.bodyHeight)
		if size.sidebarWidth > 0 {
			body = lipgloss.JoinHorizontal(lipgloss.Top, m.renderSidebar(size.sidebarWidth, size.bodyHeight), separator(size.bodyHeight), body)
		}
		if size.detailWidth > 0 {
			body = lipgloss.JoinHorizontal(lipgloss.Top, body, separator(size.bodyHeight), m.renderDetails(size.detailWidth, size.bodyHeight))
		}
	}
	stateText := fmt.Sprintf(" CONTAINERS   docker ps  |  %d listed", len(m.commandResult))
	if m.refreshErr != nil {
		switch {
		case !m.loaded:
			stateText = " Docker unavailable - retrying"
		case m.statsFailed:
			stateText = " Stats unavailable - retrying"
		default:
			stateText = " Data stale - retrying"
		}
	}
	state := line(stateText, size.width, barStyle)
	help := line(" ↑/↓ move   enter details   q quit", size.width, mutedStyle)
	return lipgloss.JoinVertical(lipgloss.Left, header, body, state, help)
}
