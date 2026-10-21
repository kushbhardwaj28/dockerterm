package docker

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/moby/moby/client"
)

// Client wraps the Docker Engine API client.
type Client struct {
	api      *client.Client
	endpoint Endpoint
	stats    *statsCollector
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
	return &Client{api: api, endpoint: endpoint, stats: newStatsCollector(api)}, nil
}

// Endpoint returns the daemon this client talks to.
func (c *Client) Endpoint() Endpoint { return c.endpoint }

// Close stops the stats streams and releases connections.
func (c *Client) Close() error {
	c.stats.close()
	return c.api.Close()
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

// Stats returns the latest resource sample for each container ID. Samples come
// from background streams (see statsCollector), so this returns immediately;
// a container seen for the first time has no sample until its stream delivers
// one, about a second later. The error names containers whose stream fails.
func (c *Client) Stats(_ context.Context, ids []string) (map[string]Stats, error) {
	return c.stats.snapshot(ids)
}

func shortID(id string) string {
	return Container{ID: id}.ShortID()
}

// Info returns the daemon version and host resources.
func (c *Client) Info(ctx context.Context) (Info, error) {
	res, err := c.api.Info(ctx, client.InfoOptions{})
	if err != nil {
		return Info{}, fmt.Errorf("info: %w", err)
	}
	i := res.Info
	return Info{Version: i.ServerVersion, OS: i.OperatingSystem, NCPU: i.NCPU, MemTotal: i.MemTotal}, nil
}

// ListImages returns one entry per tag, newest first.
func (c *Client) ListImages(ctx context.Context) ([]Image, error) {
	res, err := c.api.ImageList(ctx, client.ImageListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list images: %w", err)
	}
	var out []Image
	for _, s := range res.Items {
		out = append(out, imagesFromSummary(s)...)
	}
	slices.SortStableFunc(out, func(a, b Image) int { return b.Created.Compare(a.Created) })
	return out, nil
}

// ListVolumes returns all volumes sorted by name.
func (c *Client) ListVolumes(ctx context.Context) ([]Volume, error) {
	res, err := c.api.VolumeList(ctx, client.VolumeListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list volumes: %w", err)
	}
	out := make([]Volume, 0, len(res.Items))
	for _, v := range res.Items {
		out = append(out, volumeFromAPI(v))
	}
	slices.SortFunc(out, func(a, b Volume) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// ListNetworks returns all networks, user-defined first, then by name.
func (c *Client) ListNetworks(ctx context.Context) ([]Network, error) {
	res, err := c.api.NetworkList(ctx, client.NetworkListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list networks: %w", err)
	}
	out := make([]Network, 0, len(res.Items))
	for _, n := range res.Items {
		out = append(out, networkFromSummary(n))
	}
	slices.SortFunc(out, func(a, b Network) int {
		if a.Builtin() != b.Builtin() {
			if a.Builtin() {
				return 1
			}
			return -1
		}
		return strings.Compare(a.Name, b.Name)
	})
	return out, nil
}

// DiskUsage is `docker system df`. It can take a few seconds on big hosts.
func (c *Client) DiskUsage(ctx context.Context) (DiskUsage, error) {
	res, err := c.api.DiskUsage(ctx, client.DiskUsageOptions{Images: true, Containers: true, Volumes: true, BuildCache: true, Verbose: true})
	if err != nil {
		return DiskUsage{}, fmt.Errorf("disk usage: %w", err)
	}
	du := DiskUsage{
		Images:      res.Images.TotalSize,
		Containers:  res.Containers.TotalSize,
		Volumes:     res.Volumes.TotalSize,
		BuildCache:  res.BuildCache.TotalSize,
		VolumeSizes: map[string]int64{},
	}
	for _, v := range res.Volumes.Items {
		if v.UsageData != nil && v.UsageData.Size >= 0 {
			du.VolumeSizes[v.Name] = v.UsageData.Size
		}
	}
	return du, nil
}

// InspectContainer returns the full configuration of one container.
func (c *Client) InspectContainer(ctx context.Context, id string) (ContainerDetail, error) {
	res, err := c.api.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return ContainerDetail{}, fmt.Errorf("inspect %s: %w", shortID(id), err)
	}
	return detailFromInspect(res.Container, res.Raw), nil
}

// StartContainer is `docker start`.
func (c *Client) StartContainer(ctx context.Context, id string) error {
	_, err := c.api.ContainerStart(ctx, id, client.ContainerStartOptions{})
	return err
}

// StopContainer is `docker stop` with the container's own grace period.
func (c *Client) StopContainer(ctx context.Context, id string) error {
	_, err := c.api.ContainerStop(ctx, id, client.ContainerStopOptions{})
	return err
}

// RestartContainer is `docker restart`.
func (c *Client) RestartContainer(ctx context.Context, id string) error {
	_, err := c.api.ContainerRestart(ctx, id, client.ContainerRestartOptions{})
	return err
}

// PauseContainer is `docker pause`.
func (c *Client) PauseContainer(ctx context.Context, id string) error {
	_, err := c.api.ContainerPause(ctx, id, client.ContainerPauseOptions{})
	return err
}

// UnpauseContainer is `docker unpause`.
func (c *Client) UnpauseContainer(ctx context.Context, id string) error {
	_, err := c.api.ContainerUnpause(ctx, id, client.ContainerUnpauseOptions{})
	return err
}

// RemoveContainer is `docker rm`, with -f when force is set.
func (c *Client) RemoveContainer(ctx context.Context, id string, force bool) error {
	_, err := c.api.ContainerRemove(ctx, id, client.ContainerRemoveOptions{Force: force})
	return err
}

// RemoveImage is `docker rmi` for one tag or id.
func (c *Client) RemoveImage(ctx context.Context, ref string) error {
	_, err := c.api.ImageRemove(ctx, ref, client.ImageRemoveOptions{})
	return err
}

// RemoveVolume is `docker volume rm`.
func (c *Client) RemoveVolume(ctx context.Context, name string) error {
	_, err := c.api.VolumeRemove(ctx, name, client.VolumeRemoveOptions{})
	return err
}

// RemoveNetwork is `docker network rm`.
func (c *Client) RemoveNetwork(ctx context.Context, id string) error {
	_, err := c.api.NetworkRemove(ctx, id, client.NetworkRemoveOptions{})
	return err
}
