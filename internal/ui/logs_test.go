package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/kushbhardwaj28/dockerterm/internal/docker"
)

var logEpoch = time.Date(2026, 10, 20, 10, 0, 0, 0, time.Local)

func logLines(from, to int) []docker.LogLine {
	var out []docker.LogLine
	for i := from; i < to; i++ {
		out = append(out, docker.LogLine{Time: logEpoch.Add(time.Duration(i) * time.Second), Stream: "stdout", Text: fmt.Sprintf("INFO line %05d", i)})
	}
	return out
}

// feedLogs runs cmd until it yields a log message, applies it and returns the
// model. It skips messages from other commands batched with it.
func feedLogs(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	for _, msg := range drainLogs(cmd) {
		m, _ = step(t, m, msg)
	}
	return m
}

// drainLogs is drain() but never runs inspect/info commands twice and stops
// at the first log batch (the next one would block until more output).
func drainLogs(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, drainLogs(c)...)
		}
		return out
	}
	switch msg.(type) {
	case logBatchMsg, olderLogsMsg, logReconnectMsg, detailMsg:
		return []tea.Msg{msg}
	}
	return nil
}

func openLogs(t *testing.T, b *fakeBackend) Model {
	t.Helper()
	m, cmd := step(t, fastModel(b), tea.WindowSizeMsg{Width: 140, Height: 30}, sample())
	m = feedLogs(t, m, cmd)
	m, cmd = step(t, m, press("l"))
	return feedLogs(t, m, cmd)
}

func TestLiveLogsFollowNewOutput(t *testing.T) {
	b := &fakeBackend{logs: logLines(0, 10), live: make(chan []docker.LogLine, 4)}
	m := openLogs(t, b)
	assertContains(t, screen(m), "line 00009", "● live · 10 lines", "Enter to browse")

	b.live <- logLines(10, 12)
	m = feedLogs(t, m, waitLogs(m.logChan(t)))
	assertContains(t, screen(m), "line 00011", "● live · 12 lines")
	m.stopLogs()
}

func TestBrowsingHighlightsTheCurrentLine(t *testing.T) {
	b := &fakeBackend{logs: logLines(0, 100), live: make(chan []docker.LogLine, 4)}
	m := openLogs(t, b)
	m, _ = step(t, m, press("enter"))
	if !m.log.focus || m.log.cursor != 99 {
		t.Fatalf("Enter must focus the logs on the newest line, cursor = %d", m.log.cursor)
	}
	assertContains(t, screen(m), "LOGS", "↑↓ line")

	m, _ = step(t, m, press("up"), press("up"), press("up"))
	if c := m.selectedContainer(); c == nil || c.ID != "aaa" {
		t.Error("↑ must move the log line, not the container")
	}
	if m.log.cursor != 96 || m.log.follow {
		t.Errorf("cursor = %d follow = %v, want 96 and paused", m.log.cursor, m.log.follow)
	}
	// the current line has the highlight background (#1f3d4a)
	var current string
	for _, row := range strings.Split(m.View().Content, "\n") {
		if strings.Contains(ansi.Strip(row), "line 00096") {
			current = row
		}
	}
	if !strings.Contains(current, "48;2;31;61;74") {
		t.Errorf("current line is not highlighted: %q", current)
	}
	assertContains(t, screen(m), "‖ paused · G live")

	b.live <- logLines(100, 105)
	m = feedLogs(t, m, waitLogs(m.logChan(t)))
	assertContains(t, screen(m), "5 new")
	if m.log.cursor != 96 {
		t.Error("new lines must not move the current line")
	}

	m, _ = step(t, m, press("G"))
	assertContains(t, screen(m), "● live · 105 lines", "line 00104")
	m, _ = step(t, m, press("esc"))
	if m.log.focus {
		t.Error("Esc must give the keys back to the container list")
	}
	m.stopLogs()
}

func TestSearchJumpsBetweenMatches(t *testing.T) {
	b := &fakeBackend{logs: logLines(0, 50)}
	m := openLogs(t, b)
	m, _ = step(t, m, press("enter"), press("/"))
	for _, r := range "line 0001" {
		m, _ = step(t, m, press(string(r)))
	}
	m, _ = step(t, m, press("enter"))
	// matches: 00010..00019 → the newest is 00019
	if got := m.log.lines[m.log.view[m.log.cursor]].msg; got != "line 00019" {
		t.Fatalf("after search the current line is %q, want the newest match", got)
	}
	assertContains(t, screen(m), "/ line 0001", "10/10")
	m, _ = step(t, m, press("N"), press("N"))
	if got := m.log.lines[m.log.view[m.log.cursor]].msg; got != "line 00017" {
		t.Errorf("N N → %q, want line 00017", got)
	}
	m, _ = step(t, m, press("n"))
	assertContains(t, screen(m), "9/10")
	m, _ = step(t, m, press("esc"))
	if m.log.search != "" || !m.log.focus {
		t.Error("first Esc clears the search and keeps focus")
	}
}

func TestFiltersByLevelStreamAndText(t *testing.T) {
	lines := logLines(0, 6)
	lines[2].Text, lines[2].Stream = "ERROR db down", "stderr"
	lines[4].Text = "WARN slow"
	b := &fakeBackend{logs: lines}
	m := openLogs(t, b)
	assertContains(t, screen(m), "ERROR 1", "WARN 1")

	m, _ = step(t, m, press("enter"), press("e"))
	if len(m.log.view) != 1 {
		t.Errorf("errors only: %d lines", len(m.log.view))
	}
	assertContains(t, screen(m), "errors only", "1 of 6 lines", "db down")
	m, _ = step(t, m, press("w"))
	if len(m.log.view) != 2 {
		t.Errorf("warn+: %d lines", len(m.log.view))
	}
	m, _ = step(t, m, press("a"), press("o"), press("o"))
	if len(m.log.view) != 1 || m.log.stream != "stderr" {
		t.Errorf("stderr only: %d lines, stream %q", len(m.log.view), m.log.stream)
	}
	m, _ = step(t, m, press("c"), press("&"))
	for _, r := range "00003" {
		m, _ = step(t, m, press(string(r)))
	}
	m, _ = step(t, m, press("enter"))
	if len(m.log.view) != 1 {
		t.Errorf("& 00003: %d lines", len(m.log.view))
	}
	assertContains(t, screen(m), "& 00003", "c clear")
	m, _ = step(t, m, press("c"))
	if len(m.log.view) != 6 {
		t.Error("c must clear all filters")
	}
}

func TestLinePopupAndCopy(t *testing.T) {
	lines := logLines(0, 3)
	lines[2].Text = `{"level":"info","msg":"order created","order":{"id":1182,"total":42.5}}`
	b := &fakeBackend{logs: lines}
	m := openLogs(t, b)
	m, _ = step(t, m, press("enter"), press("enter"))
	if !m.log.popup {
		t.Fatal("Enter on a line must open it")
	}
	s := screen(m)
	assertContains(t, s, "log line", `"msg": "order created"`, `"id": 1182`, "y  copy")
	_, cmd := step(t, m, press("y"))
	if cmd == nil {
		t.Fatal("y must copy")
	}
	m, _ = step(t, m, press("esc"))
	if m.log.popup || !m.log.focus {
		t.Error("Esc closes the popup and stays in the logs")
	}
}

func TestOlderLogsLoadWhenReachingTheTop(t *testing.T) {
	b := &fakeBackend{logs: logLines(0, 1700)}
	m := openLogs(t, b)
	if n := len(m.log.lines); n != logInitialTail {
		t.Fatalf("initial lines = %d, want %d", n, logInitialTail)
	}
	m, cmd := step(t, m, press("enter"), press("g"))
	if cmd == nil {
		t.Fatal("going to the oldest line must fetch older lines")
	}
	m = feedLogs(t, m, cmd)
	if q := b.logQueries[len(b.logQueries)-1]; !q.Until.Equal(logEpoch.Add(1200*time.Second)) || q.Tail != 0 || q.Since.IsZero() {
		t.Errorf("older page query = %+v, want a since/until window without tail", q)
	}
	if got := len(m.log.lines); got < logInitialTail+logPageSize/2 {
		t.Fatalf("after one page: %d lines", got)
	}
	if e := m.log.lines[m.log.view[m.log.cursor]]; e.msg != "line 01200" {
		t.Errorf("current line moved to %q while older lines were added", e.msg)
	}
	for i := 0; i < 20 && !m.log.noOlder; i++ {
		var c tea.Cmd
		m, c = step(t, m, press("g"))
		m = feedLogs(t, m, c)
	}
	if !m.log.noOlder || len(m.log.lines) != 1700 {
		t.Errorf("lines = %d noOlder = %v, want all 1700 loaded", len(m.log.lines), m.log.noOlder)
	}
	assertContains(t, screen(m), "— start of logs —", "line 00000")
	for i := 1; i < len(m.log.lines); i++ {
		if !m.log.lines[i].Time.After(m.log.lines[i-1].Time) {
			t.Fatalf("lines out of order or duplicated at %d", i)
		}
	}
}

func TestSearchContinuesIntoOlderLines(t *testing.T) {
	b := &fakeBackend{logs: logLines(0, 1700)}
	m := openLogs(t, b)
	m, _ = step(t, m, press("enter"), press("/"))
	for _, r := range "line 00042" {
		m, _ = step(t, m, press(string(r)))
	}
	m, cmd := step(t, m, press("enter"))
	for i := 0; i < 5 && cmd != nil; i++ { // older pages until the match is found
		var next tea.Cmd
		for _, msg := range drainLogs(cmd) {
			m, next = step(t, m, msg)
		}
		cmd = next
	}
	if got := m.log.lines[m.log.view[m.log.cursor]].msg; got != "line 00042" {
		t.Errorf("current line = %q, want the match from older history", got)
	}
}

func TestStreamEndForStoppedContainer(t *testing.T) {
	b := &fakeBackend{logs: logLines(0, 3), live: make(chan []docker.LogLine)}
	m, cmd := step(t, fastModel(b), tea.WindowSizeMsg{Width: 140, Height: 30}, sample())
	m = feedLogs(t, m, cmd)
	m, _ = step(t, m, press("down"), press("down"), press("down")) // db-migrate (exited)
	m, cmd = step(t, m, press("l"))
	m = feedLogs(t, m, cmd)
	close(b.live)
	m = feedLogs(t, m, waitLogs(m.logChan(t)))
	if !m.log.ended {
		t.Fatal("stream end for a stopped container must be final")
	}
	assertContains(t, screen(m), "■ stopped · 3 lines")
}

func TestRunningContainerStreamReconnects(t *testing.T) {
	m := newModel(nil)
	m, _ = step(t, m, sample())
	m.tab, m.detailsOpen = tabLogs, true
	m.log = logState{id: "aaa", gen: 7, follow: true, streaming: true}
	m.appendLogs(logLines(0, 2))
	m, cmd := step(t, m, logBatchMsg{gen: 7, done: true})
	if cmd == nil || m.log.ended {
		t.Error("a running container's stream must reconnect")
	}
	// the resumed stream may repeat the last line; it is dropped
	m.appendLogs(logLines(1, 4))
	if n := len(m.log.lines); n != 4 {
		t.Errorf("after reconnect: %d lines, want 4 without duplicates", n)
	}
}

func TestLogBufferIsCapped(t *testing.T) {
	m := newModel(nil)
	m.log = logState{id: "aaa", follow: true}
	m.appendLogs(logLines(0, logMaxLines+10))
	if n := len(m.log.lines); n != logMaxLines || m.log.lines[0].Text != "INFO line 00010" || !m.log.trimmed || len(m.log.view) != logMaxLines {
		t.Errorf("buffer = %d lines from %q", n, m.log.lines[0].Text)
	}
}

// logChan returns the channel of the running stream by asking for the next batch.
func (m Model) logChan(t *testing.T) <-chan logBatchMsg {
	t.Helper()
	if m.log.ch == nil {
		t.Fatal("no log stream running")
	}
	return m.log.ch
}

// fastModel refreshes every millisecond so draining a refresh's tick is instant.
func fastModel(b *fakeBackend) Model {
	return New(b, Options{RefreshInterval: time.Millisecond})
}
