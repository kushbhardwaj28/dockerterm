package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/kushbhardwaj28/dockerterm/internal/docker"
)

// Logs are streamed live (docker logs -f) into a buffer the viewer can scroll
// back through. Scrolling up pauses following; reaching the oldest loaded
// lines fetches an earlier page of history.
const (
	logInitialTail = 500   // lines shown when the Logs tab opens
	logPageSize    = 1000  // lines fetched per "older" page
	logMaxLines    = 50000 // buffer cap; the oldest lines are dropped past it
	logPrefetch    = 100   // fetch older lines when this close to the top
	logReconnect   = 2 * time.Second
)

type logState struct {
	id        string
	gen       int
	cancel    context.CancelFunc
	ch        <-chan logBatchMsg // the running stream, for tests
	lines     []docker.LogLine
	follow    bool // stick to the newest line
	offset    int  // lines hidden below the view while paused
	unseen    int  // lines that arrived while paused
	streaming bool
	ended     bool // the daemon closed the stream (container stopped)
	loading   bool // an older page is in flight
	noOlder   bool // the first line of the container's log is loaded
	trimmed   bool // old lines were dropped to respect logMaxLines
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
		// it was started again: resume after the last line we have
		var since time.Time
		if n := len(m.log.lines); n > 0 {
			since = m.log.lines[n-1].Time
		}
		return m.startLogs(c.ID, since)
	}
	return m.startLogs(c.ID, time.Time{})
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
	var since time.Time
	if n := len(m.log.lines); n > 0 {
		since = m.log.lines[n-1].Time
	}
	return m.startLogs(c.ID, since)
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
	m.log.lines = append(m.log.lines, lines...)
	if !m.log.follow {
		m.log.offset += len(lines) // keep the same lines on screen
		m.log.unseen += len(lines)
	}
	if over := len(m.log.lines) - logMaxLines; over > 0 {
		m.log.lines = append([]docker.LogLine(nil), m.log.lines[over:]...)
		m.log.trimmed, m.log.noOlder = true, false
		m.log.offset = min(m.log.offset, len(m.log.lines)-1)
	}
}

// scrollLogs moves the view by delta lines (positive = towards older).
func (m *Model) scrollLogs(delta int) tea.Cmd {
	n := len(m.log.lines)
	m.log.offset = min(max(0, m.log.offset+delta), max(0, n-1))
	if m.log.offset == 0 {
		m.log.follow, m.log.unseen = true, 0
	} else {
		m.log.follow = false
	}
	return m.maybeLoadOlder()
}

func (m *Model) followLogs() {
	m.log.offset, m.log.unseen, m.log.follow = 0, 0, true
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
	if len(l.lines)-l.offset > logPrefetch {
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
func logWindow(lines []docker.LogLine) time.Duration {
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
	m.log.lines = append(append([]docker.LogLine(nil), lines...), m.log.lines...)
	return nil
}

// Rendering.

const logPrefix = 9 + 6 // "15:04:05 " + "LEVEL "

// logsStatus is the right side of the logs header line.
func (m Model) logsStatus() string {
	l := m.log
	count := fmt.Sprintf("%d lines", len(l.lines))
	switch {
	case l.ended:
		return sMuted.Render("■ stopped · " + count)
	case !l.follow:
		s := "‖ paused"
		if l.unseen > 0 {
			s += fmt.Sprintf(" · %d new", l.unseen)
		}
		return sPause.Render(s + " · F follow")
	case l.streaming:
		return sUp.Render("● following · " + count)
	}
	return sMuted.Render("reconnecting…")
}

// logsView renders the visible window: the room rows ending offset lines above
// the newest one, long messages wrapped.
func (m Model) logsView(w, room int) []string {
	l := m.log
	if len(l.lines) == 0 {
		if l.ended {
			return []string{sMuted.Render("no output")}
		}
		return []string{sMuted.Render("waiting for output…")}
	}
	end := len(l.lines) - l.offset
	var rows []string // built newest first, reversed at the end
	i := end - 1
	for ; i >= 0 && len(rows) < room; i-- {
		entry := renderLogLine(l.lines[i], w)
		for j := len(entry) - 1; j >= 0 && len(rows) < room; j-- {
			rows = append(rows, entry[j])
		}
	}
	if i < 0 && len(rows) < room { // the oldest loaded line is on screen
		switch {
		case l.loading:
			rows = append(rows, sDim.Render("↑ loading older lines…"))
		case l.noOlder:
			rows = append(rows, sDim.Render("— start of logs —"))
		case l.trimmed:
			rows = append(rows, sDim.Render(fmt.Sprintf("— older lines dropped (keeping %d) —", logMaxLines)))
		default:
			rows = append(rows, sDim.Render("↑ K for older lines"))
		}
	}
	for a, b := 0, len(rows)-1; a < b; a, b = a+1, b-1 {
		rows[a], rows[b] = rows[b], rows[a]
	}
	return rows
}

func renderLogLine(l docker.LogLine, w int) []string {
	lvl, st := logLevel(l)
	ts := "        "
	if !l.Time.IsZero() {
		ts = l.Time.Local().Format("15:04:05")
	}
	text := strings.TrimSpace(ansi.Strip(l.Text))
	// drop a leading level word we already show in its own column
	if word, rest, ok := strings.Cut(text, " "); ok && strings.EqualFold(strings.Trim(word, "[]:"), lvl) {
		text = rest
	}
	msg := strings.Split(ansi.Wrap(text, max(10, w-logPrefix), "/,"), "\n")
	out := []string{fit(sDim.Render(ts)+" "+st.Render(fit(lvl, 5))+" "+sText.Render(msg[0]), w)}
	for _, more := range msg[1:] {
		out = append(out, fit(strings.Repeat(" ", logPrefix)+sText.Render(more), w))
	}
	return out
}
