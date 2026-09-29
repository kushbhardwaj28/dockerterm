package main

import (
	"context"
	"docker_cmd"
	"fmt"
	"os"
	"time"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"

	"ui_components"
)

func main() {
	f, err := tea.LogToFile("logs/debug.log", "debug")
	if err != nil {
		fmt.Println("Error setting up log file:", err)
		os.Exit(1)
	}
	defer f.Close()
	// Create a app model
	appModel := NewModel()

	// create a new bubble tea instance
	newProgram := tea.NewProgram(appModel)

	// Run bubble tea
	if _, err := newProgram.Run(); err != nil {
		fmt.Printf("Err running bubble tea: %v\n", err)
		os.Exit(1)
	}

}

// App Model
type Model struct {
	title         string
	commandResult []docker_cmd.DockerPs
	statsByID     map[string]docker_cmd.DockerStats
	refreshErr    error
	statsFailed   bool
	dockerPsTable table.Model
	width         int
	height        int
	detailsOpen   bool
	loaded        bool
}

func NewModel() Model {
	return Model{
		title:         "DockerTerm",
		dockerPsTable: ui_components.CreateDockerPSTable([]docker_cmd.DockerPs{}),
		detailsOpen:   false,
	}
}

// Model boilerplate
func (m Model) Init() tea.Cmd {
	return refreshContainers
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "enter":
			if len(m.commandResult) > 0 {
				m.detailsOpen = !m.detailsOpen
				m.resizeTable()
			}
			return m, nil
		}
		var cmd tea.Cmd
		m.dockerPsTable, cmd = m.dockerPsTable.Update(msg)
		return m, cmd
	case tea.WindowSizeMsg:
		if m.width == 0 && msg.Width < 100 {
			m.detailsOpen = false
		}
		m.width, m.height = msg.Width, msg.Height
		m.resizeTable()
	case refreshTick:
		return m, refreshContainers
	case refreshResult:
		m.statsByID = make(map[string]docker_cmd.DockerStats, len(msg.stats))
		if msg.listErr == nil {
			selectedID := ""
			if index := m.dockerPsTable.Cursor(); index >= 0 && index < len(m.commandResult) {
				selectedID = m.commandResult[index].ID
			}
			m.commandResult = msg.containers
			m.loaded = true
			m.dockerPsTable = ui_components.SetTableData(&m.dockerPsTable, m.commandResult, nil)
			index := 0
			for position, container := range m.commandResult {
				if container.ID == selectedID {
					index = position
					break
				}
			}
			m.dockerPsTable.SetCursor(index)
			if msg.statsErr == nil {
				for _, stats := range msg.stats {
					m.statsByID[stats.ID] = stats
				}
			}
		}
		m.refreshErr = msg.listErr
		m.statsFailed = msg.listErr == nil && msg.statsErr != nil
		if m.refreshErr == nil {
			m.refreshErr = msg.statsErr
		}
		m.dockerPsTable = ui_components.SetTableData(&m.dockerPsTable, m.commandResult, m.statsByID)
		return m, tea.Tick(2*time.Second, func(time.Time) tea.Msg { return refreshTick{} })
	}

	return m, nil
}

func (m Model) View() tea.View {
	newView := tea.NewView(m.renderLayout())

	newView.AltScreen = true
	return newView
}

// Cmd
func refreshContainers() tea.Msg {
	containers, listErr := docker_cmd.ListContainers(context.Background())
	if listErr != nil {
		return refreshResult{listErr: listErr}
	}
	stats, statsErr := docker_cmd.SnapshotStats(context.Background())
	return refreshResult{containers: containers, stats: stats, statsErr: statsErr}
}

type refreshTick struct{}

type refreshResult struct {
	containers []docker_cmd.DockerPs
	stats      []docker_cmd.DockerStats
	listErr    error
	statsErr   error
}
