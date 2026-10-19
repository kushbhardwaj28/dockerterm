package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// maxConcurrentStats caps how many stats requests run at once.
const maxConcurrentStats = 8

// Client wraps the Docker Engine API client.
type Client struct {
	api      *client.Client
	endpoint Endpoint
}

// NewClient connects to the daemon described by endpoint. TLS settings
// (DOCKER_TLS_VERIFY, DOCKER_CERT_PATH) and DOCKER_API_VERSION are read from
// the environment. No request is made until the first call.
func NewClient(endpoint Endpoint) (*Client, error) {
	if strings.HasPrefix(endpoint.Host, "ssh://") {
		return nil, fmt.Errorf("ssh hosts are not supported yet (%s); forward the socket and use unix:// or tcp://", endpoint.Host)
	}
	opts := []client.Opt{client.FromEnv}
	if endpoint.Host != "" {
		opts = append(opts, client.WithHost(endpoint.Host))
	}
	api, err := client.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	return &Client{api: api, endpoint: endpoint}, nil
}

// Endpoint returns the daemon this client talks to.
func (c *Client) Endpoint() Endpoint { return c.endpoint }

// Close releases idle connections.
func (c *Client) Close() error { return c.api.Close() }

// ServerVersion returns the daemon's version, e.g. "28.5.1".
func (c *Client) ServerVersion(ctx context.Context) (string, error) {
	v, err := c.api.ServerVersion(ctx, client.ServerVersionOptions{})
	if err != nil {
		return "", err
	}
	return v.Version, nil
}

// ListContainers returns all containers, running or not, newest first.
func (c *Client) ListContainers(ctx context.Context) ([]Container, error) {
	res, err := c.api.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}
	out := make([]Container, 0, len(res.Items))
	for _, s := range res.Items {
		out = append(out, containerFromSummary(s))
	}
	return out, nil
}

// Stats takes one resource sample for each container ID, in parallel.
// It returns every sample it could get; the error joins the ones it could not.
func (c *Client) Stats(ctx context.Context, ids []string) (map[string]Stats, error) {
	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		errs []error
		sem  = make(chan struct{}, maxConcurrentStats)
		out  = make(map[string]Stats, len(ids))
	)
	for _, id := range ids {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			s, err := c.stats(ctx, id)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			out[id] = s
		})
	}
	wg.Wait()
	return out, errors.Join(errs...)
}

func (c *Client) stats(ctx context.Context, id string) (Stats, error) {
	// IncludePreviousSample makes the daemon wait for a second sample so the
	// CPU percentage can be computed, like `docker stats --no-stream`.
	res, err := c.api.ContainerStats(ctx, id, client.ContainerStatsOptions{IncludePreviousSample: true})
	if err != nil {
		return Stats{}, fmt.Errorf("stats %s: %w", shortID(id), err)
	}
	defer res.Body.Close()
	var raw container.StatsResponse
	if err := json.NewDecoder(res.Body).Decode(&raw); err != nil {
		return Stats{}, fmt.Errorf("stats %s: decode: %w", shortID(id), err)
	}
	return statsFromResponse(raw), nil
}

func shortID(id string) string {
	return Container{ID: id}.ShortID()
}
