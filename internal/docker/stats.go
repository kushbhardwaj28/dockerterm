package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// statsRetry is how long a failed stream waits before it is reopened.
const statsRetry = 3 * time.Second

// statsCollector keeps one streaming stats connection per running container,
// the way `docker stats` does, and remembers the latest sample of each.
//
// Asking the daemon for a single sample (stream=false) makes it wait for two
// readings, ~1–2 s per container and much longer on a busy host or inside the
// Docker Desktop VM. With many containers those waits add up and the refresh
// times out. A stream sends a sample about once a second without that wait,
// so reading stats becomes a map lookup.
type statsCollector struct {
	api *client.Client

	mu      sync.Mutex
	streams map[string]*statsStream
	closed  bool
}

type statsStream struct {
	cancel context.CancelFunc
	latest Stats
	have   bool
	err    error // last stream error, cleared by the next good sample
}

func newStatsCollector(api *client.Client) *statsCollector {
	return &statsCollector{api: api, streams: map[string]*statsStream{}}
}

// snapshot makes sure exactly the given containers are streamed and returns
// their latest samples. It never waits on the daemon. Containers whose stream
// is failing are reported in the error; ones still waiting for their first
// sample are simply missing from the map.
func (sc *statsCollector) snapshot(ids []string) (map[string]Stats, error) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if sc.closed {
		return nil, errors.New("stats: client closed")
	}
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
		if _, ok := sc.streams[id]; !ok {
			ctx, cancel := context.WithCancel(context.Background())
			st := &statsStream{cancel: cancel}
			sc.streams[id] = st
			go sc.run(ctx, id, st)
		}
	}
	for id, st := range sc.streams {
		if !want[id] {
			st.cancel()
			delete(sc.streams, id)
		}
	}

	out := make(map[string]Stats, len(ids))
	var errs []error
	for _, id := range ids {
		st := sc.streams[id]
		if st.have {
			out[id] = st.latest
		}
		if st.err != nil {
			errs = append(errs, st.err)
		}
	}
	return out, errors.Join(errs...)
}

// run reads one container's stream until it is cancelled, reopening it after
// errors (the daemon closes it when the container stops or restarts).
func (sc *statsCollector) run(ctx context.Context, id string, st *statsStream) {
	for ctx.Err() == nil {
		err := sc.read(ctx, id, st)
		if ctx.Err() != nil {
			return
		}
		sc.mu.Lock()
		if err != nil {
			st.err = fmt.Errorf("stats %s: %w", shortID(id), err)
		}
		sc.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-time.After(statsRetry):
		}
	}
}

func (sc *statsCollector) read(ctx context.Context, id string, st *statsStream) error {
	res, err := sc.api.ContainerStats(ctx, id, client.ContainerStatsOptions{Stream: true})
	if err != nil {
		return err
	}
	defer res.Body.Close()
	dec := json.NewDecoder(res.Body)
	for {
		var raw container.StatsResponse
		if err := dec.Decode(&raw); err != nil {
			if errors.Is(err, io.EOF) {
				return nil // the daemon ended the stream, e.g. the container stopped
			}
			return fmt.Errorf("decode: %w", err)
		}
		s := statsFromResponse(raw)
		sc.mu.Lock()
		// The first sample of a stream has no previous reading, so its CPU is 0;
		// keep the last known value instead of flashing 0%.
		if raw.PreCPUStats.SystemUsage == 0 && st.have {
			s.CPUPercent = st.latest.CPUPercent
		}
		st.latest, st.have, st.err = s, true, nil
		sc.mu.Unlock()
	}
}

func (sc *statsCollector) close() {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.closed = true
	for id, st := range sc.streams {
		st.cancel()
		delete(sc.streams, id)
	}
}
