package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/kushbhardwaj28/dockerterm/internal/docker"
)

// column describes one list column. Flexible columns share the space left
// after fixed ones; hideBelow drops the column when the list is narrower.
type column struct {
	title     string
	flex      float64
	fixed     int
	right     bool
	hideBelow int
}

type cell struct {
	text  string
	style lipgloss.Style
}

type row struct {
	key   string // stable identity used to keep the selection across refreshes
	cells []cell
}

func (m Model) columns() []column {
	switch m.view {
	case viewImages:
		return []column{
			{title: "REPOSITORY", flex: 1.6}, {title: "TAG", flex: 1}, {title: "IMAGE ID", fixed: 12, hideBelow: 60},
			{title: "SIZE", fixed: 9, right: true}, {title: "CREATED", flex: 1, hideBelow: 75}, {title: "USED", fixed: 4, right: true},
		}
	case viewVolumes:
		return []column{
			{title: "VOLUME NAME", flex: 1.6}, {title: "DRIVER", fixed: 7, hideBelow: 55},
			{title: "SIZE", fixed: 9, right: true}, {title: "USED BY", flex: 1.4},
		}
	case viewNetworks:
		return []column{
			{title: "NAME", flex: 1.4}, {title: "DRIVER", fixed: 7}, {title: "SCOPE", fixed: 6, hideBelow: 60},
			{title: "SUBNET", flex: 1.4, hideBelow: 50}, {title: "CONTAINERS", fixed: 10, right: true},
		}
	}
	return []column{
		{title: "NAME", flex: 1.1}, {title: "IMAGE", flex: 1.2, hideBelow: 70}, {title: "STATUS", flex: 1.3},
		{title: "PORTS", flex: 1.3, hideBelow: 95}, {title: "CPU", fixed: 6, right: true}, {title: "MEM", fixed: 9, right: true},
	}
}

func (m Model) rows() []row {
	switch m.view {
	case viewImages:
		return m.imageRows()
	case viewVolumes:
		return m.volumeRows()
	case viewNetworks:
		return m.networkRows()
	}
	return m.containerRows()
}

func (m Model) rowCount() int { return len(m.rows()) }

func (m Model) queryText() string { return strings.ToLower(strings.TrimSpace(m.query.Value())) }

func matches(q string, fields ...string) bool {
	if q == "" {
		return true
	}
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f), q) {
			return true
		}
	}
	return false
}

func (m Model) visibleContainers() []docker.Container {
	q := m.queryText()
	var out []docker.Container
	for _, c := range m.containers {
		switch {
		case m.filter == filterRunning && c.State != "running",
			m.filter == filterStopped && c.State == "running":
			continue
		}
		if matches(q, c.Name, c.Image, c.ComposeProject()) {
			out = append(out, c)
		}
	}
	return out
}

func (m Model) visibleImages() []docker.Image {
	var out []docker.Image
	for _, i := range m.images {
		if matches(m.queryText(), i.Repo, i.Tag, i.ShortID()) {
			out = append(out, i)
		}
	}
	return out
}

func (m Model) visibleVolumes() []docker.Volume {
	var out []docker.Volume
	for _, v := range m.volumes {
		if matches(m.queryText(), v.Name, v.Driver) {
			out = append(out, v)
		}
	}
	return out
}

func (m Model) visibleNetworks() []docker.Network {
	var out []docker.Network
	for _, n := range m.networks {
		if matches(m.queryText(), n.Name, n.Driver, n.Subnet) {
			out = append(out, n)
		}
	}
	return out
}

// Selection.

func (m Model) selectedKey() string {
	rows := m.rows()
	i := m.cursor[m.view]
	if i < 0 || i >= len(rows) {
		return ""
	}
	return rows[i].key
}

// reselect puts the cursor back on the row with the given key, if it still exists.
func (m *Model) reselect(key string) {
	for i, r := range m.rows() {
		if r.key == key {
			m.cursor[m.view] = i
			break
		}
	}
	m.clampCursor()
}

func pick[T any](items []T, i int) *T {
	if i < 0 || i >= len(items) {
		return nil
	}
	return &items[i]
}

func (m Model) selectedContainer() *docker.Container {
	if m.view != viewContainers {
		return nil
	}
	return pick(m.visibleContainers(), m.cursor[viewContainers])
}

func (m Model) selectedImage() *docker.Image {
	return pick(m.visibleImages(), m.cursor[viewImages])
}

func (m Model) selectedVolume() *docker.Volume {
	return pick(m.visibleVolumes(), m.cursor[viewVolumes])
}

func (m Model) selectedNetwork() *docker.Network {
	return pick(m.visibleNetworks(), m.cursor[viewNetworks])
}

// Cross references between resources.

func (m Model) containersUsingImage(id string) []string {
	var names []string
	for _, c := range m.containers {
		if c.ImageID == id {
			names = append(names, c.Name)
		}
	}
	return names
}

func (m Model) containersUsingVolume(name string) []string {
	var names []string
	for _, c := range m.containers {
		for _, v := range c.Volumes {
			if v == name {
				names = append(names, c.Name)
			}
		}
	}
	return names
}

func (m Model) containersOnNetwork(name string) []string {
	var names []string
	for _, c := range m.containers {
		for _, n := range c.Networks {
			if n == name {
				names = append(names, c.Name)
			}
		}
	}
	return names
}

func (m Model) volumeSize(name string) int64 {
	if m.disk != nil {
		if s, ok := m.disk.VolumeSizes[name]; ok {
			return s
		}
	}
	return -1
}

// Rows per view.

func nameCell(text string, selected bool) cell {
	if selected {
		return cell{text, sBright}
	}
	return cell{text, sText}
}

func (m Model) containerRows() []row {
	cs := m.visibleContainers()
	rows := make([]row, 0, len(cs))
	for i, c := range cs {
		sel := i == m.cursor[viewContainers]
		cpu, mem := cell{"—", sDim}, cell{"—", sDim}
		if s, ok := m.stats[c.ID]; ok {
			cpuStyle := sText
			if s.CPUPercent > 10 {
				cpuStyle = sPause
			}
			cpu = cell{pct(s.CPUPercent), cpuStyle}
			mem = cell{ubytes(s.MemoryUsage), sText}
		}
		rows = append(rows, row{key: c.ID, cells: []cell{
			nameCell(c.Name, sel),
			{c.Image, sMuted},
			{stateGlyph(c.State) + " " + c.Status, lipgloss.NewStyle().Foreground(stateColor(c.State))},
			{dash(docker.FormatPorts(c.Ports)), sMuted},
			cpu, mem,
		}})
	}
	return rows
}

func (m Model) imageRows() []row {
	imgs := m.visibleImages()
	rows := make([]row, 0, len(imgs))
	for i, img := range imgs {
		sel := i == m.cursor[viewImages]
		repo := nameCell(img.Repo, sel)
		if img.Dangling() {
			repo.style = sPause
		}
		used := len(m.containersUsingImage(img.ID))
		rows = append(rows, row{key: img.ID + "|" + img.Repo + ":" + img.Tag, cells: []cell{
			repo, {img.Tag, sMuted}, {img.ShortID(), sMuted}, {bytesStr(img.Size), sText},
			{ago(img.Created, m.lastUpdate), sMuted}, {fmt.Sprint(used), sMuted},
		}})
	}
	return rows
}

func (m Model) volumeRows() []row {
	vols := m.visibleVolumes()
	rows := make([]row, 0, len(vols))
	for i, v := range vols {
		users := m.containersUsingVolume(v.Name)
		usedBy := cell{strings.Join(users, ", "), sMuted}
		if len(users) == 0 {
			usedBy = cell{"— dangling", sPause}
		}
		rows = append(rows, row{key: v.Name, cells: []cell{
			nameCell(v.Name, i == m.cursor[viewVolumes]), {v.Driver, sMuted},
			{bytesStr(m.volumeSize(v.Name)), sText}, usedBy,
		}})
	}
	return rows
}

func (m Model) networkRows() []row {
	nets := m.visibleNetworks()
	rows := make([]row, 0, len(nets))
	for i, n := range nets {
		rows = append(rows, row{key: n.ID, cells: []cell{
			nameCell(n.Name, i == m.cursor[viewNetworks]), {n.Driver, sMuted}, {n.Scope, sMuted},
			{dash(n.Subnet), sMuted}, {fmt.Sprint(len(m.containersOnNetwork(n.Name))), sText},
		}})
	}
	return rows
}
