package ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/color"
	"sort"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// The log viewer: a highlighted current line moved with the arrow keys while
// the logs have focus, search with match highlighting, filters, and a popup
// showing one line in full (JSON pretty-printed).

var (
	cCurBg   = lipgloss.Color("#1f3d4a") // the current line
	sMatch   = lipgloss.NewStyle().Foreground(cDark).Background(cPause)
	logStyle = map[string]lipgloss.Style{
		"ERROR": sDown.Bold(true), "ERR": sDown, "WARN": sPause.Bold(true),
		"INFO": sAccent.Bold(true), "DEBUG": sMuted.Bold(true), "OUT": sDim,
	}
)

const logPrefix = 9 + 6 // "15:04:05 " + "LEVEL "

// Geometry: the same sizes renderDetail uses, so key handling can keep the
// current line on screen.

func (m Model) logPaneWidth() int {
	l := m.layout()
	switch {
	case l.detailOnly && l.hasSidebar:
		return l.w - l.sideW - 1 - 2
	case l.detailOnly:
		return l.w - 2
	}
	return l.detailW - 2
}

// logRoom is how many rows the log lines get.
func (m Model) logRoom() int {
	c := m.selectedContainer()
	if c == nil {
		return 1
	}
	return max(1, m.layout().bodyH-len(m.containerHead(c, m.logPaneWidth()))-1)
}

func (m Model) entryRows(pos, w int) int {
	return len(wrapLog(m.log.lines[m.log.view[pos]].msg, w))
}

// topFor returns the first position so that the window ends with end-1.
func (m Model) topFor(end, w, room int) int {
	rows, i := 0, end-1
	for ; i >= 0; i-- {
		rows += m.entryRows(i, w)
		if rows > room {
			return i + 1
		}
	}
	return 0
}

// keepCursorVisible scrolls so the current line is on screen.
func (m *Model) keepCursorVisible() {
	l := &m.log
	if len(l.view) == 0 {
		l.cursor, l.top = 0, 0
		return
	}
	l.cursor = min(max(0, l.cursor), len(l.view)-1)
	if l.cursor < l.top {
		l.top = l.cursor
		return
	}
	w, room := m.logPaneWidth(), m.logRoom()
	if t := m.topFor(l.cursor+1, w, room); t > l.top {
		l.top = t
	}
}

// pinFollow puts cursor and window on the newest line.
func (m *Model) pinFollow() {
	l := &m.log
	l.cursor = max(0, len(l.view)-1)
	l.top = m.topFor(len(l.view), m.logPaneWidth(), m.logRoom())
}

// moveLog moves the current line by delta (negative = older).
func (m *Model) moveLog(delta int) tea.Cmd {
	l := &m.log
	if len(l.view) == 0 {
		return m.maybeLoadOlder()
	}
	if l.follow {
		m.pinFollow()
	}
	l.cursor += delta
	if l.cursor >= len(l.view)-1 && delta > 0 {
		m.followLogs()
		return nil
	}
	l.follow = false
	m.keepCursorVisible()
	return m.maybeLoadOlder()
}

func (m *Model) followLogs() {
	m.log.follow, m.log.unseen = true, 0
	m.pinFollow()
}

// rebuildView reapplies the filters. The current line stays on keep (or the
// nearest older line) when given.
func (m *Model) rebuildView(keep *logEntry) {
	l := &m.log
	l.view = l.view[:0]
	for i, e := range l.lines {
		if l.passes(e) {
			l.view = append(l.view, i)
		}
	}
	m.rebuildMatches()
	if keep != nil && !l.follow {
		pos := sort.Search(len(l.view), func(p int) bool { return l.lines[l.view[p]].Time.After(keep.Time) })
		l.cursor = max(0, pos-1)
		m.keepCursorVisible()
	} else if l.follow {
		m.pinFollow()
	}
}

func (m *Model) rebuildMatches() {
	l := &m.log
	l.matches = l.matches[:0]
	if l.search == "" {
		return
	}
	for p, i := range l.view {
		if strings.Contains(l.lines[i].lower, l.search) {
			l.matches = append(l.matches, p)
		}
	}
}

// setFilter changes a filter while keeping the reader's place.
func (m *Model) setFilter(change func(*logState)) {
	var keep *logEntry
	if l := &m.log; !l.follow && l.cursor < len(l.view) {
		e := l.lines[l.view[l.cursor]]
		keep = &e
	}
	change(&m.log)
	m.rebuildView(keep)
}

// Search.

// searchStep jumps to the next match: dir -1 = older (N), +1 = newer (n),
// 0 = the nearest match at or above the current line (after typing a query).
func (m *Model) searchStep(dir int) tea.Cmd {
	l := &m.log
	if l.search == "" {
		return nil
	}
	cur := l.cursor
	if l.follow {
		cur = len(l.view) - 1
		if dir == 0 {
			cur++ // include the newest line
		}
	}
	var target = -1
	switch {
	case dir > 0:
		if i := sort.SearchInts(l.matches, cur+1); i < len(l.matches) {
			target = l.matches[i]
		}
	default:
		limit := cur
		if dir == 0 {
			limit = cur + 1
		}
		if i := sort.SearchInts(l.matches, limit) - 1; i >= 0 {
			target = l.matches[i]
		}
	}
	if target < 0 {
		if dir <= 0 && !l.noOlder && !l.trimmed && !l.loading {
			l.searchOlder = true
			m.setToast("searching older lines…", toastInfo)
			return m.maybeLoadOlder()
		}
		if len(l.matches) == 0 {
			m.setToast("no match for “"+l.search+"”", toastWarn)
		} else {
			m.setToast("no more matches this way", toastWarn)
		}
		return nil
	}
	l.follow, l.cursor = false, target
	m.keepCursorVisible()
	return m.maybeLoadOlder()
}

// matchIndex is the 1-based number of the match on the current line, or 0.
func (l *logState) matchIndex() int {
	i := sort.SearchInts(l.matches, l.cursor)
	if i < len(l.matches) && l.matches[i] == l.cursor && !l.follow {
		return i + 1
	}
	return 0
}

// Keys while the logs have focus.

func (m *Model) logKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	k, l := m.keys, &m.log
	if l.popup {
		switch {
		case key.Matches(msg, k.Clear), key.Matches(msg, k.Details), msg.String() == "q":
			l.popup = false
		case key.Matches(msg, k.Down):
			l.popTop++
		case key.Matches(msg, k.Up):
			l.popTop = max(0, l.popTop-1)
		case msg.String() == "y":
			return m.copyLine(), true
		}
		return nil, true
	}
	switch {
	case key.Matches(msg, k.Clear):
		switch {
		case l.search != "":
			l.search, l.matches = "", nil
		default:
			l.focus = false
		}
	case key.Matches(msg, k.NextTab), key.Matches(msg, k.PrevTab):
		l.focus = false
		return nil, false // let the tab switch happen
	case key.Matches(msg, k.Up):
		return m.moveLog(-1), true
	case key.Matches(msg, k.Down):
		return m.moveLog(1), true
	case key.Matches(msg, k.PageUp, k.LogPageUp):
		return m.moveLog(-max(1, m.logRoom()-2)), true
	case key.Matches(msg, k.PageDn, k.LogPageDown):
		return m.moveLog(max(1, m.logRoom()-2)), true
	case key.Matches(msg, k.Top):
		return m.moveLog(-len(l.view)), true
	case key.Matches(msg, k.Bottom), key.Matches(msg, k.Follow):
		m.followLogs()
	case key.Matches(msg, k.Details):
		if len(l.view) > 0 {
			if l.follow {
				m.pinFollow()
			}
			l.popup, l.popTop = true, 0
		}
	case msg.String() == "/":
		return m.openLogInput(inputSearch, l.search), true
	case msg.String() == "&":
		return m.openLogInput(inputGrep, l.grep), true
	case msg.String() == "n":
		return m.searchStep(1), true
	case msg.String() == "N":
		return m.searchStep(-1), true
	case msg.String() == "e":
		m.setFilter(func(s *logState) { s.level = toggleLevel(s.level, levelError) })
	case msg.String() == "w":
		m.setFilter(func(s *logState) { s.level = toggleLevel(s.level, levelWarn) })
	case msg.String() == "a":
		m.setFilter(func(s *logState) { s.level = levelAll })
	case msg.String() == "o":
		m.setFilter(func(s *logState) {
			s.stream = map[string]string{"": "stdout", "stdout": "stderr", "stderr": ""}[s.stream]
		})
	case msg.String() == "c":
		m.setFilter(func(s *logState) { s.level, s.stream, s.grep, s.search = levelAll, "", "", "" })
	case msg.String() == "y":
		return m.copyLine(), true
	default:
		return nil, false
	}
	return nil, true
}

func toggleLevel(cur, want logLevelFilter) logLevelFilter {
	if cur == want {
		return levelAll
	}
	return want
}

func (m *Model) openLogInput(kind logInputKind, value string) tea.Cmd {
	m.logInputKind = kind
	m.logInput.SetValue(value)
	m.logInput.CursorEnd()
	m.logInput.Placeholder = map[logInputKind]string{inputSearch: "search loaded lines", inputGrep: "show only lines containing…"}[kind]
	return m.logInput.Focus()
}

// logInputKey handles typing in the search / grep prompt. Results update as
// you type; Enter keeps them, Esc clears them.
func (m *Model) logInputKey(msg tea.KeyPressMsg) tea.Cmd {
	kind := m.logInputKind
	switch msg.String() {
	case "esc":
		m.logInput.SetValue("")
		m.logInput.Blur()
		m.logInputKind = inputNone
		m.applyLogInput(kind, "")
		return nil
	case "enter":
		m.logInput.Blur()
		m.logInputKind = inputNone
		if kind == inputSearch {
			return m.searchStep(0)
		}
		return nil
	}
	var cmd tea.Cmd
	m.logInput, cmd = m.logInput.Update(msg)
	m.applyLogInput(kind, m.logInput.Value())
	if kind == inputSearch {
		if len(m.log.matches) > 0 {
			return tea.Batch(cmd, m.searchStep(0))
		}
	}
	return cmd
}

func (m *Model) applyLogInput(kind logInputKind, v string) {
	v = strings.ToLower(strings.TrimSpace(v))
	switch kind {
	case inputSearch:
		m.log.search = v
		m.rebuildMatches()
	case inputGrep:
		m.setFilter(func(s *logState) { s.grep = v })
	}
}

func (m *Model) copyLine() tea.Cmd {
	l := &m.log
	if len(l.view) == 0 {
		return nil
	}
	pos := l.cursor
	if l.follow {
		pos = len(l.view) - 1
	}
	e := l.lines[l.view[pos]]
	m.setToast("copied line to clipboard", toastOK)
	return tea.SetClipboard(e.Text)
}

// Rendering.

// logsHeader is the line above the logs: command, level counts, state.
func (m Model) logsHeader(name string, w int) string {
	l := m.log
	var right []string
	if l.nErr > 0 {
		right = append(right, sDown.Render(fmt.Sprintf("ERROR %d", l.nErr)))
	}
	if l.nWarn > 0 {
		right = append(right, sPause.Render(fmt.Sprintf("WARN %d", l.nWarn)))
	}
	right = append(right, m.logsStatus())
	status := strings.Join(right, sDim.Render(" · "))
	left := fit(sDim.Render("docker logs -f "+name), max(0, w-lipgloss.Width(status)-2))
	return spread(left, status, w)
}

// logsStatus is the follow/pause state with the line count.
func (m Model) logsStatus() string {
	l := m.log
	count := fmt.Sprintf("%d lines", len(l.lines))
	if len(l.view) != len(l.lines) {
		count = fmt.Sprintf("%d of %d lines", len(l.view), len(l.lines))
	}
	switch {
	case l.ended && l.follow:
		return sMuted.Render("■ stopped · " + count)
	case !l.follow:
		s := "‖ paused"
		if l.unseen > 0 {
			s += fmt.Sprintf(" · %d new", l.unseen)
		}
		return sPause.Render(s + " · G live")
	case l.streaming:
		return sUp.Render("● live · " + count)
	}
	return sMuted.Render("reconnecting…")
}

// logsBar is the line under the header: active filters, the search prompt
// and match position, or a hint how to focus the logs.
func (m Model) logsBar(w int) string {
	l := m.log
	var parts []string
	if m.logInputKind != inputNone {
		label := map[logInputKind]string{inputSearch: "/ ", inputGrep: "& "}[m.logInputKind]
		in := m.logInput
		in.SetWidth(max(10, w/2))
		parts = append(parts, sAccent.Bold(true).Render(label)+in.View())
	} else if l.search != "" {
		pos := "–"
		if i := l.matchIndex(); i > 0 {
			pos = fmt.Sprint(i)
		}
		parts = append(parts, sAccent.Render("/ ")+sText.Render(l.search)+sMuted.Render(fmt.Sprintf("  %s/%d  n next · N prev", pos, len(l.matches))))
	}
	var chips []string
	switch l.level {
	case levelError:
		chips = append(chips, "errors only")
	case levelWarn:
		chips = append(chips, "warn+")
	}
	if l.stream != "" {
		chips = append(chips, l.stream)
	}
	if l.grep != "" {
		chips = append(chips, "& "+l.grep)
	}
	for _, c := range chips {
		parts = append(parts, badge(c, cAccent))
	}
	if len(chips) > 0 {
		parts = append(parts, sDim.Render("c clear"))
	}
	if len(parts) == 0 {
		if l.focus {
			return sDim.Render("↑↓ line · / search · & filter · e w a level · o stream · Enter open · y copy · Esc back")
		}
		return sDim.Render("Enter to browse · / search inside after Enter")
	}
	return fit(strings.Join(parts, "  "), w)
}

// logsView renders the visible window of the log viewer.
func (m Model) logsView(w, room int) []string {
	l := m.log
	if len(l.view) == 0 {
		switch {
		case len(l.lines) > 0:
			return []string{sMuted.Render("no lines match the filters · c to clear")}
		case l.ended:
			return []string{sMuted.Render("no output")}
		}
		return []string{sMuted.Render("waiting for output…")}
	}
	top, cursor := l.top, l.cursor
	if l.follow {
		top, cursor = m.topFor(len(l.view), w, room), len(l.view)-1
	}
	top = min(max(0, top), len(l.view)-1)
	var rows []string
	if top == 0 && l.view[0] == 0 {
		switch {
		case l.loading:
			rows = append(rows, sDim.Render("↑ loading older lines…"))
		case l.noOlder:
			rows = append(rows, sDim.Render("— start of logs —"))
		case l.trimmed:
			rows = append(rows, sDim.Render(fmt.Sprintf("— older lines dropped (keeping %d) —", logMaxLines)))
		}
	}
	for p := top; p < len(l.view) && len(rows) < room; p++ {
		current := l.focus && p == cursor
		for _, r := range renderLogEntry(l.lines[l.view[p]], w, l.search, current) {
			if len(rows) < room {
				rows = append(rows, r)
			}
		}
	}
	return rows
}

func wrapLog(msg string, w int) []string {
	return strings.Split(ansi.Wrap(msg, max(10, w-logPrefix), "/,"), "\n")
}

// renderLogEntry draws one entry, wrapped, with search matches marked and a
// full-width highlight when it is the current line.
func renderLogEntry(e logEntry, w int, search string, current bool) []string {
	ts := "        "
	if !e.Time.IsZero() {
		ts = e.Time.Local().Format("15:04:05")
	}
	var bg color.Color
	text := sText
	if current {
		bg, text = cCurBg, sBright.Bold(false)
	}
	var out []string
	for i, piece := range wrapLog(e.msg, w) {
		var segs []seg
		if i == 0 {
			segs = []seg{{ts + " ", sDim}, {fit(e.lvl, 5) + " ", logStyle[e.lvl]}}
		} else {
			segs = []seg{{strings.Repeat(" ", logPrefix), text}}
		}
		segs = append(segs, highlight(piece, search, text)...)
		if pad := w - segsWidth(segs); pad > 0 {
			segs = append(segs, seg{strings.Repeat(" ", pad), text})
		}
		out = append(out, ansi.Truncate(renderSegs(segs, bg), w, "…"))
	}
	return out
}

// highlight splits text into plain and matching segments.
func highlight(text, search string, base lipgloss.Style) []seg {
	if search == "" {
		return []seg{{text, base}}
	}
	var out []seg
	lower := strings.ToLower(text)
	for {
		i := strings.Index(lower, search)
		if i < 0 {
			break
		}
		out = append(out, seg{text[:i], base}, seg{text[i : i+len(search)], sMatch})
		text, lower = text[i+len(search):], lower[i+len(search):]
	}
	return append(out, seg{text, base})
}

// renderLogPopup shows the current line in full: metadata, then the message,
// pretty-printed when it is JSON.
func (m Model) renderLogPopup(w, h int) string {
	l := m.log
	pos := l.cursor
	if l.follow {
		pos = len(l.view) - 1
	}
	e := l.lines[l.view[pos]]
	inner := w - 6
	meta := sMuted.Render(e.Time.Local().Format("2006-01-02 15:04:05.000 MST")) + "  " +
		logStyle[e.lvl].Render(e.lvl) + "  " + sDim.Render(e.Stream)
	body := prettyLog(e.msg, inner)
	maxBody := max(3, h-10)
	top := min(l.popTop, max(0, len(body)-maxBody))
	shown := body[top:min(len(body), top+maxBody)]
	more := ""
	if len(body) > maxBody {
		more = sDim.Render(fmt.Sprintf("  lines %d–%d of %d", top+1, top+len(shown), len(body)))
	}
	lines := append([]string{
		spread(sAccent.Bold(true).Render("log line")+more, sKeyChip.Render("y")+sMuted.Render(" copy  ")+sKeyChip.Render("Esc")+sMuted.Render(" close"), inner),
		meta, "",
	}, shown...)
	return dialogStyle(cAccent, w).Render(strings.Join(lines, "\n"))
}

// prettyLog indents JSON messages and colors their keys; other text wraps.
func prettyLog(msg string, w int) []string {
	trimmed := strings.TrimSpace(msg)
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		var buf bytes.Buffer
		if json.Valid([]byte(trimmed)) && json.Indent(&buf, []byte(trimmed), "", "  ") == nil {
			var out []string
			for _, line := range strings.Split(buf.String(), "\n") {
				if g := jsonKey.FindStringSubmatch(line); g != nil {
					line = g[1] + sAccent.Render(g[2]) + sText.Render(g[3])
				} else {
					line = sText.Render(line)
				}
				out = append(out, strings.Split(ansi.Wrap(line, w, ",/"), "\n")...)
			}
			return out
		}
	}
	return strings.Split(sText.Render(ansi.Wrap(msg, w, ",/ ")), "\n")
}
