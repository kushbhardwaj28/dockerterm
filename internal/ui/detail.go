package ui

import (
	"fmt"
	"image/color"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/kushbhardwaj28/dockerterm/internal/docker"
)

// renderDetail draws the right-hand pane for whatever is selected.
func (m Model) renderDetail(w, h int) string {
	inner := w - 2 // one space of padding on each side
	var head, body []string
	switch m.view {
	case viewContainers:
		head, body = m.containerDetail(inner, h)
	case viewImages:
		head, body = m.imageDetail(inner)
	case viewVolumes:
		head, body = m.volumeDetail(inner)
	case viewNetworks:
		head, body = m.networkDetail(inner)
	}
	if head == nil {
		return box("\n "+sMuted.Render("nothing selected"), w, h)
	}
	// Scroll the body; logs are anchored to the bottom like a terminal.
	room := max(0, h-len(head))
	if m.logsShown() {
		// logsView already rendered exactly the visible window
		body = body[:min(len(body), room)]
	} else {
		top := min(m.detailTop, max(0, len(body)-room))
		body = body[top:]
	}
	lines := make([]string, 0, h)
	for _, l := range append(head, body...) {
		lines = append(lines, " "+l)
	}
	return box(strings.Join(lines, "\n"), w, h)
}

// header is the name + badge, a muted sub line, and the action chips.
func detailHeader(name, badgeText string, badgeColor color.Color, sub string, actions [][3]string, w int) []string {
	first := sBright.Render(name)
	if badgeText != "" {
		first += "  " + badge(badgeText, badgeColor)
	}
	lines := []string{"", fit(first, w), fit(sMuted.Render(sub), w)}
	// action chips, wrapped onto as many lines as needed
	var cur string
	for _, a := range actions {
		label := sText.Render(a[1])
		if a[2] == "danger" {
			label = sDown.Render(a[1])
		}
		chip := sAccent.Bold(true).Render(a[0]) + " " + label
		if cur != "" && lipgloss.Width(cur)+2+lipgloss.Width(chip) > w {
			lines = append(lines, fit(cur, w))
			cur = ""
		}
		if cur != "" {
			cur += "  "
		}
		cur += chip
	}
	if cur != "" {
		lines = append(lines, fit(cur, w))
	}
	return append(lines, "")
}

func kv(label, value string, w int) string {
	return fit(sMuted.Render(fit(label, 12))+sText.Render(value), w)
}

func kvLines(pairs [][2]string, w int) []string {
	var out []string
	for _, p := range pairs {
		if p[1] == "" {
			continue
		}
		// long values wrap under themselves
		wrapped := strings.Split(ansi.Wrap(p[1], max(10, w-12), "/,"), "\n")
		out = append(out, kv(p[0], wrapped[0], w))
		for _, more := range wrapped[1:] {
			out = append(out, kv("", more, w))
		}
	}
	return out
}

// Containers.

func (m Model) containerDetail(w, h int) ([]string, []string) {
	c := m.selectedContainer()
	if c == nil {
		return nil, nil
	}
	sub := c.ShortID() + " · " + c.Image
	if p := c.ComposeProject(); p != "" {
		sub += " · compose: " + p
	}
	startStop := [3]string{"s", "start", ""}
	if c.State == "running" || c.State == "paused" || c.State == "restarting" {
		startStop = [3]string{"x", "stop", ""}
	}
	pause := [3]string{"p", "pause", ""}
	if c.State == "paused" {
		pause[1] = "unpause"
	}
	head := detailHeader(c.Name, strings.ToUpper(c.State), stateColor(c.State), sub, [][3]string{
		startStop, {"r", "restart", ""}, pause, {"l", "logs", ""}, {"e", "exec", ""}, {"d", "remove", "danger"},
	}, w)
	head = append(head, m.renderTabs(w), hline(w))
	if m.tab == tabLogs {
		status := m.logsStatus()
		left := fit(sDim.Render("docker logs -f "+c.Name), max(0, w-lipgloss.Width(status)-2))
		head = append(head, spread(left, status, w))
	}

	var body []string
	switch m.tab {
	case tabInfo:
		body = m.infoTab(c, w)
	case tabLogs:
		body = m.logsView(w, max(1, h-len(head)-1))
	case tabStats:
		body = m.statsTab(c, w)
	case tabEnv:
		body = m.envTab(w)
	case tabMounts:
		body = m.mountsTab(w)
	case tabInspect:
		body = m.inspectTab(w)
	}
	return head, append([]string{""}, body...)
}

func (m Model) renderTabs(w int) string {
	var parts []string
	for i, name := range tabNames {
		if detailTab(i) == m.tab {
			parts = append(parts, lipgloss.NewStyle().Foreground(cBright).Bold(true).Underline(true).UnderlineColor(cAccent).Render(name))
		} else {
			parts = append(parts, sMuted.Render(name))
		}
	}
	hint := "z full width"
	if m.zoom {
		hint = "z back · ↑↓ next container"
	}
	return spread(strings.Join(parts, "   "), sDim.Render(hint), w)
}

func (m Model) infoTab(c *docker.Container, w int) []string {
	pairs := [][2]string{
		{"Status", c.Status},
		{"Health", c.Health},
		{"Image", c.Image},
		{"Command", c.Command},
		{"Created", ago(c.Created, m.lastUpdate)},
		{"Ports", docker.FormatPorts(c.Ports)},
		{"Networks", strings.Join(c.Networks, ", ")},
	}
	if d := m.detail; d != nil {
		var ips []string
		for _, n := range slices.Sorted(maps.Keys(d.IPs)) {
			ips = append(ips, d.IPs[n])
		}
		pairs = append(pairs,
			[2]string{"IP address", strings.Join(ips, ", ")},
			[2]string{"Restart", d.RestartPolicy},
			[2]string{"Restarts", fmt.Sprint(d.RestartCount)},
			[2]string{"Workdir", d.WorkingDir},
			[2]string{"User", d.User},
		)
	}
	pairs = append(pairs, [2]string{"Compose", c.ComposeProject()})
	if s, ok := m.stats[c.ID]; ok {
		mem := ubytes(s.MemoryUsage)
		if s.MemoryLimit > 0 {
			mem += " / " + ubytes(s.MemoryLimit)
		}
		pairs = append(pairs, [2]string{"CPU", pct(s.CPUPercent)}, [2]string{"Memory", mem})
	}
	return kvLines(pairs, w)
}

var levelPattern = regexp.MustCompile(`(?i)\b(fatal|panic|error|err|warn|warning|info|debug|trace)\b`)

// logLevel guesses a level from the message so lines can be colored.
func logLevel(l docker.LogLine) (string, lipgloss.Style) {
	lvl := strings.ToUpper(levelPattern.FindString(l.Text))
	switch lvl {
	case "FATAL", "PANIC", "ERROR", "ERR":
		return "ERROR", sDown.Bold(true)
	case "WARN", "WARNING":
		return "WARN", sPause.Bold(true)
	case "DEBUG", "TRACE":
		return "DEBUG", sMuted.Bold(true)
	case "INFO":
		return "INFO", sAccent.Bold(true)
	}
	if l.Stream == "stderr" {
		return "ERR", sDown
	}
	return "OUT", sDim
}

func (m Model) statsTab(c *docker.Container, w int) []string {
	s, ok := m.stats[c.ID]
	if !ok {
		if c.State != "running" {
			return []string{sMuted.Render("no live stats — the container is " + c.State)}
		}
		return []string{sMuted.Render("collecting stats…")}
	}
	cpuColor := cUp
	if s.CPUPercent > 10 {
		cpuColor = cPause
	}
	hist := m.cpuHist[c.ID]
	span := time.Duration(len(hist)) * m.opts.RefreshInterval
	lines := []string{
		spread(sMuted.Render("CPU"), sText.Render(pct(s.CPUPercent)), w),
		meter(min(100, s.CPUPercent), w, cpuColor),
		sAccent.Render(sparkline(hist, w)),
		sDim.Render("last " + span.Round(time.Second).String()),
		"",
	}
	mem := ubytes(s.MemoryUsage)
	if s.MemoryLimit > 0 {
		mem += fmt.Sprintf(" / %s (%.0f%%)", ubytes(s.MemoryLimit), s.MemoryPercent())
	}
	lines = append(lines,
		spread(sMuted.Render("MEMORY"), sText.Render(mem), w),
		meter(s.MemoryPercent(), w, cAccent),
		"",
	)
	restarts := "—"
	if m.detail != nil {
		restarts = fmt.Sprint(m.detail.RestartCount)
	}
	cw := (w - 1) / 2
	tile := func(label, value string) string {
		return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cLine).Padding(0, 1).Width(cw).
			Render(sDim.Render(label) + "\n" + sText.Render(value))
	}
	grid := lipgloss.JoinVertical(lipgloss.Left,
		lipgloss.JoinHorizontal(lipgloss.Top, tile("NET I/O", ubytes(s.NetRx)+" / "+ubytes(s.NetTx)), " ", tile("BLOCK I/O", ubytes(s.BlockRead)+" / "+ubytes(s.BlockWrite))),
		lipgloss.JoinHorizontal(lipgloss.Top, tile("PIDS", fmt.Sprint(s.PIDs)), " ", tile("RESTARTS", restarts)),
	)
	return append(lines, strings.Split(grid, "\n")...)
}

var secretKey = regexp.MustCompile(`(?i)(pass|secret|token|key|credential|auth)`)

const masked = "••••••••"

func isSecret(k, v string) bool { return v != "" && secretKey.MatchString(k) }

// envJSON matches one "NAME=value" entry of the Env array in inspect output.
var envJSON = regexp.MustCompile(`^(\s*")([A-Za-z_][A-Za-z0-9_.]*)=(.*)("[,]?)$`)

// maskEnvJSON hides secret values in inspect output, like the Env tab.
func maskEnvJSON(line string) string {
	g := envJSON.FindStringSubmatch(line)
	if g == nil || !isSecret(g[2], g[3]) {
		return line
	}
	return g[1] + g[2] + "=" + masked + g[4]
}

func (m Model) envTab(w int) []string {
	if m.detail == nil {
		return []string{sMuted.Render("loading…")}
	}
	var out []string
	secrets := 0
	for _, e := range m.detail.Env {
		k, v, _ := strings.Cut(e, "=")
		val := sText.Render(v)
		if isSecret(k, v) {
			secrets++
			if m.reveal {
				val = sPause.Render(v)
			} else {
				val = sDim.Render(masked)
			}
		}
		// long values wrap instead of being cut off
		lines := strings.Split(ansi.Wrap(sAccent.Render(k)+sDim.Render("=")+val, w, "/,;&"), "\n")
		for _, l := range lines {
			out = append(out, fit(l, w))
		}
	}
	if len(out) == 0 {
		return []string{sMuted.Render("no environment variables")}
	}
	if secrets > 0 {
		note := fmt.Sprintf("%d secret value(s) hidden · v to show", secrets)
		if m.reveal {
			note = fmt.Sprintf("%d secret value(s) visible · v to hide", secrets)
		}
		out = append([]string{sDim.Render(note), ""}, out...)
	}
	return out
}

func (m Model) mountsTab(w int) []string {
	if m.detail == nil {
		return []string{sMuted.Render("loading…")}
	}
	if len(m.detail.Mounts) == 0 {
		return []string{sMuted.Render("no mounts")}
	}
	var out []string
	for _, mt := range m.detail.Mounts {
		src := mt.Source
		if mt.Name != "" {
			src = mt.Name
		}
		mode := "ro"
		if mt.RW {
			mode = "rw"
		}
		card := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cLine).Padding(0, 1).Width(w).Render(
			sMuted.Render(mt.Type) + "  " + sText.Render(src) + "\n" +
				sDim.Render("→ ") + sText.Render(mt.Destination) + sMuted.Render(" ("+mode+")"))
		out = append(out, strings.Split(card, "\n")...)
	}
	return out
}

var jsonKey = regexp.MustCompile(`^(\s*)("[^"]+")(:.*)$`)

func (m Model) inspectTab(w int) []string {
	if m.detail == nil || m.detail.Raw == "" {
		return []string{sMuted.Render("loading…")}
	}
	var out []string
	for _, l := range strings.Split(m.detail.Raw, "\n") {
		if !m.reveal {
			l = maskEnvJSON(l)
		}
		if g := jsonKey.FindStringSubmatch(l); g != nil {
			l = g[1] + sAccent.Render(g[2]) + sText.Render(g[3])
		} else {
			l = sText.Render(l)
		}
		out = append(out, fit(l, w))
	}
	return out
}

// Images, volumes, networks.

func (m Model) imageDetail(w int) ([]string, []string) {
	img := m.selectedImage()
	if img == nil {
		return nil, nil
	}
	users := m.containersUsingImage(img.ID)
	b, col := "IN USE", cAccent
	switch {
	case img.Dangling():
		b, col = "DANGLING", cPause
	case len(users) == 0:
		b, col = "UNUSED", cPause
	}
	head := detailHeader(img.Ref(), b, col, img.ID, [][3]string{{"d", "remove", "danger"}}, w)
	head = append(head, hline(w))
	return head, append([]string{""}, kvLines([][2]string{
		{"Repository", img.Repo}, {"Tag", img.Tag}, {"Image ID", img.ShortID()},
		{"Size", bytesStr(img.Size)}, {"Created", ago(img.Created, m.lastUpdate)},
		{"Containers", dash(strings.Join(users, ", "))},
	}, w)...)
}

func (m Model) volumeDetail(w int) ([]string, []string) {
	v := m.selectedVolume()
	if v == nil {
		return nil, nil
	}
	users := m.containersUsingVolume(v.Name)
	b, col := "IN USE", cAccent
	if len(users) == 0 {
		b, col = "DANGLING", cPause
	}
	head := detailHeader(v.Name, b, col, "driver: "+v.Driver, [][3]string{{"d", "remove", "danger"}}, w)
	head = append(head, hline(w))
	return head, append([]string{""}, kvLines([][2]string{
		{"Name", v.Name}, {"Driver", v.Driver}, {"Size", bytesStr(m.volumeSize(v.Name))},
		{"Used by", dash(strings.Join(users, ", "))}, {"Mountpoint", v.Mountpoint},
		{"Scope", v.Scope}, {"Created", ago(v.Created, m.lastUpdate)},
	}, w)...)
}

func (m Model) networkDetail(w int) ([]string, []string) {
	n := m.selectedNetwork()
	if n == nil {
		return nil, nil
	}
	actions := [][3]string{{"d", "remove", "danger"}}
	if n.Builtin() {
		actions = nil
	}
	head := detailHeader(n.Name, strings.ToUpper(n.Driver), cAccent, "driver: "+n.Driver+" · scope: "+n.Scope, actions, w)
	head = append(head, hline(w))
	return head, append([]string{""}, kvLines([][2]string{
		{"Name", n.Name}, {"ID", n.ID[:min(12, len(n.ID))]}, {"Driver", n.Driver}, {"Scope", n.Scope},
		{"Subnet", dash(n.Subnet)}, {"Internal", fmt.Sprint(n.Internal)}, {"Attachable", fmt.Sprint(n.Attachable)},
		{"Containers", dash(strings.Join(m.containersOnNetwork(n.Name), ", "))},
	}, w)...)
}
