package ui_components

import (
	"docker_cmd"
	"strings"

	"charm.land/bubbles/v2/table"
	"charm.land/lipgloss/v2"
)

var columns = []table.Column{
	{Title: "Name", Width: 0},
	{Title: "Image", Width: 0},
	{Title: "Status", Width: 0},
	{Title: "Ports", Width: 0},
	{Title: "CPU", Width: 0},
	{Title: "Mem", Width: 0},
}

func CreateDockerPSTable(data []docker_cmd.DockerPs) table.Model {
	t := table.New(
		table.WithColumns(columns),
		table.WithRows(mapDockerDataToTableRow(data, nil)),
		table.WithFocused(true),
	)

	s := table.DefaultStyles()

	s.Header = s.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("240")).
		BorderBottom(true).
		Bold(false)
	s.Selected = s.Selected.
		Foreground(lipgloss.Color("229")).
		Background(lipgloss.Color("57")).
		Bold(false)

	t.SetStyles(s)
	return t
}

func SetTableData(t *table.Model, data []docker_cmd.DockerPs, statsByID map[string]docker_cmd.DockerStats) table.Model {
	t.SetRows(mapDockerDataToTableRow(data, statsByID))
	return *t
}

func mapDockerDataToTableRow(data []docker_cmd.DockerPs, statsByID map[string]docker_cmd.DockerStats) []table.Row {
	tableRows := []table.Row{}

	for idx := range data {
		entry := data[idx]
		Names, Image, Status, Ports := entry.Names, entry.Image, entry.Status, entry.Ports
		stats := statsByID[entry.ID]
		memory := strings.TrimSpace(strings.SplitN(stats.MemUsage, "/", 2)[0])
		rowData := table.Row{
			returnDefaultHypenString(Names),
			returnDefaultHypenString(Image),
			returnDefaultHypenString(Status),
			returnDefaultHypenString(Ports),
			returnDefaultHypenString(stats.CPUPerc),
			returnDefaultHypenString(memory),
		}
		tableRows = append(tableRows, rowData)
	}
	return tableRows
}

func returnDefaultHypenString(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func UpdateTableDimensions(t *table.Model, width, height int) {
	visible := 6
	if width < 90 {
		visible--
	}
	if width < 65 {
		visible--
	}
	usable := max(0, width-visible*2)
	nameWidth := usable * 24 / 100
	statusWidth := usable * 22 / 100
	cpuWidth := usable * 14 / 100
	imageWidth, portsWidth := 0, 0
	if width >= 65 {
		imageWidth = usable * 20 / 100
	}
	if width >= 90 {
		portsWidth = usable * 10 / 100
	}
	memoryWidth := usable - nameWidth - imageWidth - statusWidth - portsWidth - cpuWidth

	t.SetColumns([]table.Column{
		{Title: "Name", Width: nameWidth},
		{Title: "Image", Width: imageWidth},
		{Title: "Status", Width: statusWidth},
		{Title: "Ports", Width: portsWidth},
		{Title: "CPU", Width: cpuWidth},
		{Title: "Mem", Width: memoryWidth},
	})
	t.SetWidth(max(0, width))
	t.SetHeight(max(2, height))
}
