package docker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/client"
)

// LogQuery selects a window of a container's output.
type LogQuery struct {
	Tail  int       // last N lines of the window; 0 means all
	Since time.Time // only lines at or after this time
	Until time.Time // only lines before this time (history pages)
}

// Logs returns one page of history, oldest first. To page further back, pass
// the time of the oldest line you have as Until.
func (c *Client) Logs(ctx context.Context, id string, q LogQuery) ([]LogLine, error) {
	var out []LogLine
	err := c.streamLogs(ctx, id, q, false, func(l LogLine) { out = append(out, l) })
	return out, err
}

// FollowLogs sends the last q.Tail lines and then new lines as they are
// written, in batches, until ctx is cancelled or the container stops (the
// daemon then ends the stream and FollowLogs returns nil).
func (c *Client) FollowLogs(ctx context.Context, id string, q LogQuery, emit func([]LogLine)) error {
	lines := make(chan LogLine, 1024)
	errc := make(chan error, 1)
	go func() {
		errc <- c.streamLogs(ctx, id, q, true, func(l LogLine) { lines <- l })
		close(lines)
	}()

	// Batch lines so a chatty container doesn't flood the UI with messages.
	const maxBatch, flushEvery = 500, 100 * time.Millisecond
	tick := time.NewTicker(flushEvery)
	defer tick.Stop()
	var batch []LogLine
	flush := func() {
		if len(batch) > 0 {
			emit(batch)
			batch = nil
		}
	}
	for {
		select {
		case l, ok := <-lines:
			if !ok {
				flush()
				err := <-errc
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
			batch = append(batch, l)
			if len(batch) >= maxBatch {
				flush()
			}
		case <-tick.C:
			flush()
		}
	}
}

func (c *Client) streamLogs(ctx context.Context, id string, q LogQuery, follow bool, emit func(LogLine)) error {
	// A container with a TTY sends one raw stream; others multiplex stdout/stderr.
	insp, err := c.api.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return fmt.Errorf("logs %s: %w", shortID(id), err)
	}
	tty := insp.Container.Config != nil && insp.Container.Config.Tty
	opts := client.ContainerLogsOptions{ShowStdout: true, ShowStderr: true, Timestamps: true, Follow: follow, Tail: "all"}
	if q.Tail > 0 {
		opts.Tail = strconv.Itoa(q.Tail)
	}
	if !q.Since.IsZero() {
		opts.Since = unixNano(q.Since)
	}
	if !q.Until.IsZero() {
		opts.Until = unixNano(q.Until)
	}
	rc, err := c.api.ContainerLogs(ctx, id, opts)
	if err != nil {
		return fmt.Errorf("logs %s: %w", shortID(id), err)
	}
	defer rc.Close()
	if err := readLogStream(rc, tty, emit); err != nil && ctx.Err() == nil {
		return fmt.Errorf("logs %s: %w", shortID(id), err)
	}
	return nil
}

// unixNano formats t the way the API's since/until accept: seconds.nanoseconds.
func unixNano(t time.Time) string {
	return fmt.Sprintf("%d.%09d", t.Unix(), t.Nanosecond())
}

// readLogStream decodes a log stream incrementally, calling emit per line.
// Without a TTY the stream is framed: an 8-byte header (stream type, 3 zero
// bytes, big-endian payload length) before each chunk; lines can span chunks.
func readLogStream(r io.Reader, tty bool, emit func(LogLine)) error {
	if tty {
		return splitLines(r, "stdout", emit)
	}
	br := bufio.NewReaderSize(r, 32*1024)
	carry := map[byte]*bytes.Buffer{1: {}, 2: {}}
	var head [8]byte
	for {
		if _, err := io.ReadFull(br, head[:]); err != nil {
			for k, b := range carry { // a last line without a newline
				if b.Len() > 0 {
					emit(parseLogLine(b.String(), streamName(k)))
				}
			}
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil
			}
			return err
		}
		kind := head[0]
		if kind == 0 {
			kind = 1 // stdin is reported on stdout
		}
		buf, ok := carry[kind]
		if !ok {
			buf = carry[1]
		}
		if _, err := io.CopyN(buf, br, int64(binary.BigEndian.Uint32(head[4:]))); err != nil {
			return err
		}
		for {
			i := bytes.IndexByte(buf.Bytes(), '\n')
			if i < 0 {
				break
			}
			line := string(buf.Next(i + 1))
			emit(parseLogLine(strings.TrimRight(line, "\r\n"), streamName(kind)))
		}
	}
}

func streamName(kind byte) string {
	if kind == 2 {
		return "stderr"
	}
	return "stdout"
}

func splitLines(r io.Reader, stream string, emit func(LogLine)) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		emit(parseLogLine(strings.TrimRight(sc.Text(), "\r"), stream))
	}
	return sc.Err()
}

func parseLogLine(text, stream string) LogLine {
	l := LogLine{Stream: stream, Text: text}
	if ts, rest, ok := strings.Cut(text, " "); ok {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			l.Time, l.Text = t, rest
		}
	}
	return l
}
