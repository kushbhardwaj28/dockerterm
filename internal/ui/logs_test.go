package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

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
	assertContains(t, screen(m), "line 00009", "● following · 10 lines")

	b.live <- logLines(10, 12)
	m = feedLogs(t, m, waitLogs(m.logChan(t)))
	assertContains(t, screen(m), "line 00011", "● following · 12 lines")
	m.stopLogs()
}

func TestScrollingBackPausesAndFollowResumes(t *testing.T) {
	b := &fakeBackend{logs: logLines(0, 100), live: make(chan []docker.LogLine, 4)}
	m := openLogs(t, b)
	m, _ = step(t, m, press("K"), press("K"))
	s := screen(m)
	assertContains(t, s, "‖ paused · F follow", "line 00093")
	if strings.Contains(s, "line 00099") {
		t.Error("scrolling back must hide the newest lines")
	}

	b.live <- logLines(100, 105)
	m = feedLogs(t, m, waitLogs(m.logChan(t)))
	s = screen(m)
	assertContains(t, s, "5 new", "line 00093")
	if strings.Contains(s, "line 00104") {
		t.Error("new lines must not move a paused view")
	}

	m, _ = step(t, m, press("F"))
	assertContains(t, screen(m), "● following · 105 lines", "line 00104")
	m.stopLogs()
}

func TestOlderLogsLoadWhenReachingTheTop(t *testing.T) {
	b := &fakeBackend{logs: logLines(0, 1700)}
	m := openLogs(t, b)
	if n := len(m.log.lines); n != logInitialTail {
		t.Fatalf("initial lines = %d, want %d", n, logInitialTail)
	}
	// page back to the oldest loaded lines
	var cmd tea.Cmd
	for i := 0; i < 40 && cmd == nil; i++ {
		m, cmd = step(t, m, press("["))
	}
	if cmd == nil {
		t.Fatal("reaching the top must fetch older lines")
	}
	m = feedLogs(t, m, cmd)
	if got := len(m.log.lines); got < logInitialTail+logPageSize/2 {
		t.Fatalf("after one page: %d lines, want at least %d", got, logInitialTail+logPageSize/2)
	}
	if q := b.logQueries[len(b.logQueries)-1]; !q.Until.Equal(logEpoch.Add(1200*time.Second)) || q.Tail != 0 || q.Since.IsZero() {
		t.Errorf("older page query = %+v, want a since/until window without tail", q)
	}
	for i := 0; i < 80; i++ { // keep going: the last page is short, so that's the start
		var c tea.Cmd
		m, c = step(t, m, press("["))
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
	m.stopLogs()
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
	m.log = logState{id: "aaa", gen: 7, follow: true, streaming: true, lines: logLines(0, 2)}
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
	if n := len(m.log.lines); n != logMaxLines || m.log.lines[0].Text != "INFO line 00010" || !m.log.trimmed {
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
