package ui

import (
	"context"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/kushbhardwaj28/dockerterm/internal/docker"
)

// Logs are streamed live (docker logs -f) into a buffer that can be browsed:
// a highlighted current line, search, level/stream/text filters and a popup
// for one line. This file holds the stream and the buffer; logview.go the
// viewer.
const (
	logInitialTail = 500   // lines shown when the Logs tab opens
	logPageSize    = 1000  // lines fetched per "older" page
	logMaxLines    = 50000 // buffer cap; the oldest lines are dropped past it
	logPrefetch    = 100   // fetch older lines when this close to the top
	logReconnect   = 2 * time.Second
)

// logEntry is one buffered line plus what the viewer needs about it.
type logEntry struct {
	docker.LogLine
	lvl   string // ERROR, WARN, INFO, DEBUG, ERR (stderr), OUT
	msg   string // display text: ANSI stripped, leading level word dropped
	lower string // msg lowercased, for search and filters
}

func newLogEntry(l docker.LogLine) logEntry {
	lvl, _ := logLevel(l)
	text := strings.TrimSpace(ansi.Strip(l.Text))
	if word, rest, ok := strings.Cut(text, " "); ok && strings.EqualFold(strings.Trim(word, "[]:"), lvl) {
		text = rest
	}
	return logEntry{LogLine: l, lvl: lvl, msg: text, lower: strings.ToLower(text)}
}

type logLevelFilter int

const (
	levelAll logLevelFilter = iota
	levelWarn
	levelError
)

type logState struct {
	id        string
	gen       int
	cancel    context.CancelFunc
	ch        <-chan logBatchMsg // the running stream
	lines     []logEntry
	streaming bool
	ended     bool // the daemon closed the stream (container stopped)
	loading   bool // an older page is in flight
	noOlder   bool // the first line of the container's log is loaded
	trimmed   bool // old lines were dropped to respect logMaxLines

	// viewer (logview.go)
	view    []int // indexes into lines that pass the filters
	follow  bool  // stick to the newest line
	cursor  int   // highlighted position in view
	top     int   // first position in view on screen
	unseen  int   // lines that arrived while not following
	focus   bool  // keys drive the log viewer, not the container list
	level   logLevelFilter
	stream  string // "", "stdout" or "stderr"
	grep    string // only lines containing this (lowercase)
	search  string // highlighted and jumped to with n / N (lowercase)
	matches []int  // view positions matching search
	nErr    int
	nWarn   int
	popup   bool // the current line opened in a dialog
	popTop  int
	// searchOlder continues an N search once an older page arrives
	searchOlder bool
}

// logInputKind says what the log text input is collecting.
type logInputKind int

const (
	inputNone logInputKind = iota
	inputSearch
	inputGrep
)

func newLogInput() textinput.Model {
	ti := textinput.New()
	ti.Prompt = ""
	ti.CharLimit = 128
	return ti
}

type (
	logBatchMsg struct {
		gen   int
		lines []docker.LogLine
		done  bool
		err   error
		ch    <-chan logBatchMsg
	}
	olderLogsMsg struct {
		gen   int
		lines []docker.LogLine
		err   error
		start bool // the page reaches back to the container's first line
	}
	logReconnectMsg struct{ gen int }
)

// ensureLogs starts streaming the selected container's logs when the Logs tab
// is showing, and stops streaming when it is not.
func (m *Model) ensureLogs() tea.Cmd {
	c := m.selectedContainer()
	if m.view != viewContainers || m.tab != tabLogs || c == nil || (!m.detailsOpen && !m.zoom) {
		m.stopLogs()
		return nil
	}
	if m.log.id == c.ID && m.log.streaming {
		return nil
	}
	if m.log.id == c.ID && m.log.ended {
		if !c.Running() {
			return nil
		}
		return m.startLogs(c.ID, m.lastLogTime()) // started again: resume
	}
	return m.startLogs(c.ID, time.Time{})
}

func (m *Model) lastLogTime() time.Time {
	if n := len(m.log.lines); n > 0 {
		return m.log.lines[n-1].Time
	}
	return time.Time{}
}

// startLogs opens a follow stream. A zero since starts fresh with the last
// logInitialTail lines; a non-zero since resumes after a reconnect.
func (m *Model) startLogs(id string, since time.Time) tea.Cmd {
	if m.log.cancel != nil {
		m.log.cancel()
	}
	m.logGen++
	if since.IsZero() || m.log.id != id {
		m.log = logState{id: id, follow: true}
	}
	gen := m.logGen
	ctx, cancel := context.WithCancel(context.Background())
	m.log.gen, m.log.cancel, m.log.streaming, m.log.ended = gen, cancel, true, false

	ch := make(chan logBatchMsg, 16)
	b := m.backend
	q := docker.LogQuery{Tail: logInitialTail, Since: since}
	if !since.IsZero() {
		q.Tail = 0
	}
	go func() {
		defer close(ch)
		err := b.FollowLogs(ctx, id, q, func(lines []docker.LogLine) {
			select {
			case ch <- logBatchMsg{gen: gen, lines: lines}:
			case <-ctx.Done():
			}
		})
		select {
		case ch <- logBatchMsg{gen: gen, done: true, err: err}:
		case <-ctx.Done():
		}
	}()
	return waitLogs(ch)
}

func waitLogs(ch <-chan logBatchMsg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		msg.ch = ch
		return msg
	}
}

func (m *Model) stopLogs() {
	if m.log.cancel != nil {
		m.log.cancel()
	}
	m.log = logState{}
	m.logInput.Blur()
	m.logInputKind = inputNone
}

func (m *Model) handleLogBatch(msg logBatchMsg) tea.Cmd {
	if msg.gen != m.log.gen {
		return nil // from a stream we already stopped
	}
	m.log.ch = msg.ch
	m.appendLogs(msg.lines)
	if !msg.done {
		return waitLogs(msg.ch)
	}
	m.log.streaming = false
	if msg.err != nil {
		m.setToast(msg.err.Error(), toastErr)
	}
	// A running container's stream can end on restart; pick up where we left off.
	if c := m.selectedContainer(); c != nil && c.ID == m.log.id && c.Running() {
		gen := m.log.gen
		return tea.Tick(logReconnect, func(time.Time) tea.Msg { return logReconnectMsg{gen: gen} })
	}
	m.log.ended = true
	return nil
}

func (m *Model) handleLogReconnect(msg logReconnectMsg) tea.Cmd {
	if msg.gen != m.log.gen || m.log.streaming {
		return nil
	}
	c := m.selectedContainer()
	if c == nil || c.ID != m.log.id || m.tab != tabLogs {
		return nil
	}
	if !c.Running() {
		m.log.ended = true
		return nil
	}
	return m.startLogs(c.ID, m.lastLogTime())
}

// appendLogs adds new lines, skipping ones a reconnect delivered twice.
func (m *Model) appendLogs(lines []docker.LogLine) {
	if n := len(m.log.lines); n > 0 {
		last := m.log.lines[n-1]
		i := 0
		for i < len(lines) && !lines[i].Time.IsZero() && (lines[i].Time.Before(last.Time) ||
			(lines[i].Time.Equal(last.Time) && lines[i].Text == last.Text)) {
			i++
		}
		lines = lines[i:]
	}
	if len(lines) == 0 {
		return
	}
	l := &m.log
	for _, raw := range lines {
		e := newLogEntry(raw)
		l.lines = append(l.lines, e)
		l.count(e, 1)
		if l.passes(e) {
			l.view = append(l.view, len(l.lines)-1)
			if l.search != "" && strings.Contains(e.lower, l.search) {
				l.matches = append(l.matches, len(l.view)-1)
			}
			if !l.follow {
				l.unseen++
			}
		}
	}
	if over := len(l.lines) - logMaxLines; over > 0 {
		l.trimmed, l.noOlder = true, false
		m.replaceLines(l.lines[over:])
	}
}

// replaceLines swaps the buffer and rebuilds everything derived from it,
// keeping the highlighted line on the same entry where it still exists.
func (m *Model) replaceLines(lines []logEntry) {
	l := &m.log
	var keep *logEntry
	if !l.follow && l.cursor >= 0 && l.cursor < len(l.view) {
		e := l.lines[l.view[l.cursor]]
		keep = &e
	}
	l.lines = append([]logEntry(nil), lines...)
	l.nErr, l.nWarn = 0, 0
	for _, e := range l.lines {
		l.count(e, 1)
	}
	m.rebuildView(keep)
}

func (l *logState) count(e logEntry, d int) {
	switch e.lvl {
	case "ERROR", "ERR":
		l.nErr += d
	case "WARN":
		l.nWarn += d
	}
}

// passes reports whether an entry survives the level, stream and text filters.
func (l *logState) passes(e logEntry) bool {
	switch l.level {
	case levelError:
		if e.lvl != "ERROR" && e.lvl != "ERR" {
			return false
		}
	case levelWarn:
		if e.lvl != "ERROR" && e.lvl != "ERR" && e.lvl != "WARN" {
			return false
		}
	}
	if l.stream != "" && e.Stream != l.stream {
		return false
	}
	return l.grep == "" || strings.Contains(e.lower, l.grep)
}

// maybeLoadOlder fetches an earlier page once the view nears the oldest line.
//
// The daemon applies "tail" to the whole log before "until", so tail+until
// cannot page backwards. Instead we ask for a time window just before the
// oldest line we have, sized from the observed line rate, and widen it until
// it holds a page or reaches the container's creation time.
func (m *Model) maybeLoadOlder() tea.Cmd {
	l := &m.log
	if l.loading || l.noOlder || l.trimmed || l.id == "" || len(l.lines) == 0 {
		return nil
	}
	if l.top < len(l.view) && len(l.view) > 0 && l.view[min(l.top, len(l.view)-1)] > logPrefetch && !l.searchOlder {
		return nil
	}
	until := l.lines[0].Time
	if until.IsZero() {
		l.noOlder = true
		return nil
	}
	var created time.Time
	if c := m.selectedContainer(); c != nil && c.ID == l.id {
		created = c.Created
	}
	window := logWindow(l.lines)
	l.loading = true
	b, id, gen := m.backend, l.id, l.gen
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		for attempt := 0; ; attempt++ {
			since := until.Add(-window)
			atStart := attempt >= 12 || (!created.IsZero() && !since.After(created.Add(-time.Second)))
			if atStart {
				since = time.Time{} // everything before until
			}
			lines, err := b.Logs(ctx, id, docker.LogQuery{Since: since, Until: until})
			if err != nil || atStart || len(lines) >= logPageSize/2 {
				return olderLogsMsg{gen: gen, lines: lines, err: err, start: atStart}
			}
			window *= 4
		}
	}
}

// logWindow guesses how far back one page of lines reaches.
func logWindow(lines []logEntry) time.Duration {
	span := lines[len(lines)-1].Time.Sub(lines[0].Time)
	w := time.Duration(float64(span) / float64(len(lines)) * logPageSize * 1.5)
	return min(max(w, 5*time.Second), 24*time.Hour)
}

func (m *Model) handleOlderLogs(msg olderLogsMsg) tea.Cmd {
	if msg.gen != m.log.gen || len(m.log.lines) == 0 {
		return nil
	}
	m.log.loading = false
	if msg.err != nil {
		m.setToast(msg.err.Error(), toastErr)
		return nil
	}
	// "until" includes the boundary: drop lines we already have.
	first := m.log.lines[0]
	lines := msg.lines
	for len(lines) > 0 {
		last := lines[len(lines)-1]
		if last.Time.After(first.Time) || (last.Time.Equal(first.Time) && last.Text == first.Text) {
			lines = lines[:len(lines)-1]
			continue
		}
		break
	}
	complete := msg.start
	if keep := 5 * logPageSize; len(lines) > keep { // a dense window: take its newest part
		lines, complete = lines[len(lines)-keep:], false
	}
	if room := logMaxLines - len(m.log.lines); len(lines) > room {
		lines, complete, m.log.trimmed = lines[len(lines)-max(0, room):], false, true
	}
	m.log.noOlder = complete

	// Prepend: positions in the view shift by the number of new visible lines.
	older := make([]logEntry, 0, len(lines))
	for _, raw := range lines {
		older = append(older, newLogEntry(raw))
	}
	before := len(m.log.view)
	cursor, top := m.log.cursor, m.log.top
	all := append(older, m.log.lines...)
	m.log.lines = all
	m.log.nErr, m.log.nWarn = 0, 0
	for _, e := range all {
		m.log.count(e, 1)
	}
	m.rebuildView(nil)
	shift := len(m.log.view) - before
	m.log.cursor, m.log.top = cursor+shift, top+shift

	if m.log.searchOlder {
		m.log.searchOlder = false
		return m.searchStep(-1)
	}
	return nil
}
