package ui

import (
	"fmt"
	"image/color"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// layout is the space each region gets for the current window.
type layout struct {
	w, h, bodyH            int
	sideW, listW, detailW  int
	detailOnly, hasSidebar bool
}

const (
	sidebarWidth = 22
	chromeLines  = 3 // header + status line + key hints
	listChrome   = 4 // toolbar, rule, column header, rule
)

func (m Model) layout() layout {
	w, h := m.width, m.height
	if w <= 0 {
		w = 80
	}
	if h <= 0 {
		h = 24
	}
	l := layout{w: w, h: h, bodyH: max(1, h-chromeLines)}
	if w >= 90 {
		l.sideW, l.hasSidebar = sidebarWidth, true
	}
	if m.detailsOpen && m.rowCount() > 0 {
		if w >= 110 {
			l.detailW = min(72, max(42, w*37/100))
		} else {
			l.detailOnly = true
		}
	}
	l.listW = w
	if l.hasSidebar {
		l.listW -= l.sideW + 1
	}
	if l.detailW > 0 {
		l.listW -= l.detailW + 1
	}
	return l
}

// listRows is how many list rows fit on screen.
func (m Model) listRows() int { return max(1, m.layout().bodyH-listChrome) }

func (m Model) render() string {
	l := m.layout()
	var body string
	switch {
	case l.detailOnly:
		body = m.renderDetail(l.w, l.bodyH)
		if l.hasSidebar {
			body = lipgloss.JoinHorizontal(lipgloss.Top, m.renderSidebar(l.sideW, l.bodyH), vline(l.bodyH), m.renderDetail(l.w-l.sideW-1, l.bodyH))
		}
	default:
		parts := []string{}
		if l.hasSidebar {
			parts = append(parts, m.renderSidebar(l.sideW, l.bodyH), vline(l.bodyH))
		}
		parts = append(parts, m.renderList(l.listW, l.bodyH))
		if l.detailW > 0 {
			parts = append(parts, vline(l.bodyH), m.renderDetail(l.detailW, l.bodyH))
		}
		body = lipgloss.JoinHorizontal(lipgloss.Top, parts...)
	}
	screen := lipgloss.JoinVertical(lipgloss.Left,
		m.renderHeader(l.w), body, m.renderStatus(l.w), m.renderKeys(l.w))
	return m.overlay(screen, l)
}

// Segments let a whole bar share one background even across styled pieces.

type seg struct {
	text  string
	style lipgloss.Style
}

func segsWidth(segs []seg) int {
	n := 0
	for _, s := range segs {
		n += lipgloss.Width(s.text)
	}
	return n
}

func renderSegs(segs []seg, bg color.Color) string {
	var b strings.Builder
	for _, s := range segs {
		st := s.style
		if bg != nil && !hasBackground(st) {
			st = st.Background(bg)
		}
		b.WriteString(st.Render(s.text))
	}
	return b.String()
}

func hasBackground(st lipgloss.Style) bool {
	c := st.GetBackground()

	_, none := c.(lipgloss.NoColor)
	return !none
}

// bar lays out left and right segments on one full-width line.
func bar(left, right []seg, w int, bg color.Color) string {
	for len(right) > 0 && segsWidth(left)+segsWidth(right)+1 > w {
		right = right[1:]
	}
	gap := w - segsWidth(left) - segsWidth(right)
	if gap < 0 {
		return ansi.Truncate(renderSegs(left, bg), w, "…")
	}
	return renderSegs(left, bg) + renderSegs([]seg{{strings.Repeat(" ", gap), lipgloss.NewStyle()}}, bg) + renderSegs(right, bg)
}

// Header.

func (m Model) renderHeader(w int) string {
	var running, paused, stopped int
	var cpu float64
	var mem uint64
	for _, c := range m.containers {
		switch c.State {
		case "running":
			running++
		case "paused":
			paused++
		default:
			stopped++
		}
		if s, ok := m.stats[c.ID]; ok {
			cpu += s.CPUPercent
			mem += s.MemoryUsage
		}
	}
	left := []seg{{" ◆ dockterm", sAccent.Bold(true)}}
	if m.opts.Endpoint != "" {
		left = append(left, seg{"   ctx ", sMuted}, seg{m.opts.Endpoint, sText})
	}
	if m.info.Version != "" {
		left = append(left, seg{"   engine ", sMuted}, seg{m.info.Version, sText})
	}
	memText := ubytes(mem)
	if m.info.MemTotal > 0 {
		memText += " / " + bytesStr(m.info.MemTotal)
	}
	counts := []seg{
		{"running ", sMuted}, {fmt.Sprint(running), sUp.Bold(true)},
		{"  stopped ", sMuted}, {fmt.Sprint(stopped), sDown.Bold(true)},
		{"  paused ", sMuted}, {fmt.Sprint(paused), sPause.Bold(true)},
	}
	cpuSeg := []seg{{"  cpu ", sMuted}, {pct(cpu), sText}}
	memSeg := []seg{{"  mem ", sMuted}, {memText, sText}}
	help := []seg{{"   ", sText}, {"?", sAccent.Bold(true)}, {" help ", sMuted}}
	// Drop the least useful groups first when the terminal is narrow.
	for _, drop := range []int{-1, 2, 1, 0, 3} {
		switch drop {
		case 2:
			memSeg = nil
		case 1:
			cpuSeg = nil
		case 0:
			counts = nil
		case 3:
			left = left[:1]
		}
		right := slices.Concat(counts, cpuSeg, memSeg, help)
		if segsWidth(left)+segsWidth(right)+1 <= w {
			return bar(left, right, w, cBarBg)
		}
	}
	right := help
	return bar(left, right, w, cBarBg)
}

// Sidebar.

func (m Model) renderSidebar(w, h int) string {
	counts := [numViews]int{len(m.containers), len(m.images), len(m.volumes), len(m.networks)}
	labels := [numViews]string{"Containers", "Images", "Volumes", "Networks"}
	lines := []string{"", fit(sDim.Render("  RESOURCES"), w)}
	for v := range numViews {
		left := []seg{{fmt.Sprintf("  %d  ", v+1), sDim}, {labels[v], sText}}
		var bg color.Color
		if m.view == v {
			left[1].style = sBright
			bg = cSelBg
		}
		right := []seg{{fmt.Sprintf("%d ", counts[v]), sMuted}}
		if !m.loaded {
			right[0].text = "· "
		}
		lines = append(lines, bar(left, right, w, bg))
	}

	var footer []string
	if m.disk != nil {
		footer = []string{
			hline(w),
			fit(sDim.Render("  DISK USAGE"), w),
			diskLine("images", m.disk.Images, w),
			diskLine("volumes", m.disk.Volumes, w),
			diskLine("containers", m.disk.Containers, w),
			diskLine("cache", m.disk.BuildCache, w),
		}
	}
	for len(lines)+len(footer) < h {
		lines = append(lines, "")
	}
	return box(strings.Join(append(lines, footer...), "\n"), w, h)
}

func diskLine(label string, n int64, w int) string {
	return bar([]seg{{"  " + label, sMuted}}, []seg{{bytesStr(n) + " ", sText}}, w, nil)
}

// List.

func (m Model) renderList(w, h int) string {
	cols := m.columns()
	widths := columnWidths(cols, w-3)
	rows := m.rows()

	lines := []string{m.renderToolbar(w, len(rows)), hline(w)}
	var head []seg
	head = append(head, seg{"   ", sMuted})
	for i, c := range cols {
		if widths[i] == 0 {
			continue
		}
		t := fit(c.title, widths[i])
		if c.right {
			t = fitRight(c.title, widths[i])
		}
		head = append(head, seg{t + "  ", sMuted})
	}
	lines = append(lines, fit(renderSegs(head, nil), w), hline(w))

	switch {
	case !m.loaded && m.refreshErr != nil:
		lines = append(lines, "", fit(sPause.Render("   Docker unavailable - retrying"), w))
	case !m.loaded:
		lines = append(lines, "", fit(sMuted.Render("   Loading…"), w))
	case len(rows) == 0 && m.queryText() != "":
		lines = append(lines, "", fit(sMuted.Render("   no matches — press Esc to clear the filter"), w))
	case len(rows) == 0:
		lines = append(lines, "", fit(sMuted.Render("   no "+viewNames[m.view]), w))
	}

	off := m.offset[m.view]
	end := min(len(rows), off+max(1, h-listChrome))
	for i := off; i < end; i++ {
		lines = append(lines, renderRow(rows[i], cols, widths, w, i == m.cursor[m.view]))
	}
	return box(strings.Join(lines, "\n"), w, h)
}

func (m Model) renderToolbar(w, n int) string {
	left := []seg{{" " + viewNames[m.view], sBright}, {fmt.Sprintf(" [%d]  ", n), sMuted}}
	if m.view == viewContainers {
		for i, name := range filterNames {
			if stateFilter(i) == m.filter {
				left = append(left, seg{" " + name + " ", lipgloss.NewStyle().Foreground(cDark).Background(cAccent).Bold(true)})
			} else {
				left = append(left, seg{" " + name + " ", sMuted})
			}
			left = append(left, seg{" ", sMuted})
		}
	}
	var q string
	switch {
	case m.query.Focused():
		qi := m.query
		qi.SetWidth(max(8, min(28, w-segsWidth(left)-6)))
		q = qi.View()
	case m.query.Value() != "":
		q = sText.Render(m.query.Value())
	default:
		q = sDim.Render("filter")
	}
	right := []seg{{"/ ", sAccent.Bold(true)}, {q, lipgloss.NewStyle()}, {" ", sMuted}}
	return bar(left, right, w, nil)
}

// columnWidths shares avail cells between the visible columns.
func columnWidths(cols []column, avail int) []int {
	widths := make([]int, len(cols))
	var flexSum float64
	fixed, visible := 0, 0
	for i, c := range cols {
		if avail < c.hideBelow {
			continue
		}
		visible++
		if c.fixed > 0 {
			widths[i] = c.fixed
			fixed += c.fixed
		} else {
			flexSum += c.flex
		}
	}
	space := avail - fixed - 2*visible
	if space < 0 {
		space = 0
	}
	last, used := -1, 0
	for i, c := range cols {
		if avail < c.hideBelow || c.fixed > 0 {
			continue
		}
		widths[i] = max(1, int(float64(space)*c.flex/flexSum))
		used += widths[i]
		last = i
	}
	if last >= 0 {
		widths[last] += space - used
	}
	return widths
}

func renderRow(r row, cols []column, widths []int, w int, selected bool) string {
	var bg color.Color
	marker := seg{"   ", sText}
	if selected {
		bg = cSelBg
		marker = seg{" › ", sAccent.Bold(true)}
	}
	segs := []seg{marker}
	for i, c := range cols {
		if widths[i] == 0 || i >= len(r.cells) {
			continue
		}
		t := fit(r.cells[i].text, widths[i])
		if c.right {
			t = fitRight(r.cells[i].text, widths[i])
		}
		segs = append(segs, seg{t, r.cells[i].style}, seg{"  ", sText})
	}
	if pad := w - segsWidth(segs); pad > 0 {
		segs = append(segs, seg{strings.Repeat(" ", pad), sText})
	}
	return ansi.Truncate(renderSegs(segs, bg), w, "")
}

// Status line and key hints.

func (m Model) renderStatus(w int) string {
	mode := "NORMAL"
	switch {
	case m.confirm != nil:
		mode = "CONFIRM"
	case m.help:
		mode = "HELP"
	case m.query.Focused() || m.query.Value() != "":
		mode = "FILTER"
	}
	left := []seg{{" " + mode + " ", sMode}, {"  ", sText}}

	text, style := m.toast, sUp
	switch m.toastKind {
	case toastInfo:
		style = sAccent
	case toastWarn:
		style = sPause
	case toastErr:
		style = sDown
	}
	if m.refreshErr != nil {
		style = sPause
		switch {
		case !m.loaded:
			text = "Docker unavailable - retrying: " + m.refreshErr.Error()
		case m.statsFailed:
			text = "Stats unavailable - retrying: " + m.refreshErr.Error()
		default:
			text = "Data stale - retrying: " + m.refreshErr.Error()
		}
	}
	left = append(left, seg{strings.Join(strings.Fields(text), " "), style})

	right := []seg{{fmt.Sprintf("auto-refresh %s", m.opts.RefreshInterval), sMuted}}
	if !m.lastUpdate.IsZero() {
		right = append(right, seg{" · " + m.lastUpdate.Format("15:04:05") + " ", sMuted})
	}
	return bar(left, right, w, cBarBg)
}

func (m Model) renderKeys(w int) string {
	var b strings.Builder
	b.WriteString(" ")
	for _, h := range m.footerHints() {
		b.WriteString(sKeyChip.Render(h.key))
		b.WriteString(" ")
		b.WriteString(sMuted.Render(h.label))
		b.WriteString("  ")
	}
	return fit(b.String(), w)
}

// Overlays.

func (m Model) overlay(screen string, l layout) string {
	var dialog string
	switch {
	case m.confirm != nil:
		dialog = m.renderConfirm(min(56, l.w-4))
	case m.help:
		dialog = m.renderHelp(min(76, l.w-4))
	default:
		return screen
	}
	dw, dh := lipgloss.Width(dialog), lipgloss.Height(dialog)
	return lipgloss.NewCompositor(
		lipgloss.NewLayer(screen),
		lipgloss.NewLayer(dialog).X(max(0, (l.w-dw)/2)).Y(max(0, (l.h-dh)/2)).Z(1),
	).Render()
}

func dialogStyle(border color.Color, w int) lipgloss.Style {
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border).Padding(1, 2).Width(w)
}

func (m Model) renderConfirm(w int) string {
	c := m.confirm
	inner := w - 6
	buttons := sKeyChip.Render("Esc") + sMuted.Render(" cancel   ") +
		lipgloss.NewStyle().Foreground(cDark).Background(cDown).Bold(true).Padding(0, 1).Render("y") +
		lipgloss.NewStyle().Foreground(cDown).Bold(true).Render(" "+c.verb)
	content := strings.Join([]string{
		sDown.Bold(true).Render(c.title),
		"",
		lipgloss.NewStyle().Foreground(cText).Width(inner).Render(c.body),
		"",
		sMuted.Render("$ " + c.cmd),
		"",
		fitRight(buttons, inner),
	}, "\n")
	return dialogStyle(cDown, w).Render(content)
}

func (m Model) renderHelp(w int) string {
	inner := w - 6
	colW := (inner - 4) / 2
	rows := m.helpRows()
	half := (len(rows) + 1) / 2
	lines := []string{spread(sAccent.Bold(true).Render("keybindings"), sKeyChip.Render("Esc")+sMuted.Render(" close"), inner), ""}
	for i := range half {
		left := helpCell(rows[i], colW)
		right := ""
		if i+half < len(rows) {
			right = helpCell(rows[i+half], colW)
		}
		lines = append(lines, left+"    "+right)
	}
	return dialogStyle(cAccent, w).Render(strings.Join(lines, "\n"))
}

func helpCell(r [2]string, w int) string {
	return fit(sAccent.Render(fit(r[0], 9))+sText.Render(r[1]), w)
}
