package docker

import (
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
)

// containerFromSummary converts an Engine API container summary.
func containerFromSummary(s container.Summary) Container {
	c := Container{
		ID:      s.ID,
		Image:   s.Image,
		ImageID: s.ImageID,
		Command: s.Command,
		State:   string(s.State),
		Status:  s.Status,
		Created: time.Unix(s.Created, 0),
		Labels:  s.Labels,
	}
	if len(s.Names) > 0 {
		c.Name = strings.TrimPrefix(s.Names[0], "/")
	}
	if s.Health != nil && s.Health.Status != container.NoHealthcheck {
		c.Health = string(s.Health.Status)
	}
	for _, p := range s.Ports {
		port := Port{PrivatePort: p.PrivatePort, PublicPort: p.PublicPort, Type: p.Type}
		if p.IP.IsValid() {
			port.IP = p.IP.String()
		}
		c.Ports = append(c.Ports, port)
	}
	for _, mp := range s.Mounts {
		if mp.Name != "" {
			c.Volumes = append(c.Volumes, mp.Name)
		}
	}
	if s.NetworkSettings != nil {
		c.Networks = slices.Sorted(maps.Keys(s.NetworkSettings.Networks))
	}
	return c
}

// statsFromResponse turns a raw stats sample into percentages and byte counts,
// using the same formulas as `docker stats` on Linux.
func statsFromResponse(r container.StatsResponse) Stats {
	s := Stats{
		CPUPercent:  cpuPercent(r.PreCPUStats, r.CPUStats),
		MemoryUsage: memoryUsage(r.MemoryStats),
		MemoryLimit: r.MemoryStats.Limit,
		PIDs:        r.PidsStats.Current,
	}
	for _, n := range r.Networks {
		s.NetRx += n.RxBytes
		s.NetTx += n.TxBytes
	}
	for _, e := range r.BlkioStats.IoServiceBytesRecursive {
		switch strings.ToLower(e.Op) {
		case "read":
			s.BlockRead += e.Value
		case "write":
			s.BlockWrite += e.Value
		}
	}
	return s
}

func cpuPercent(prev, cur container.CPUStats) float64 {
	cpuDelta := float64(cur.CPUUsage.TotalUsage) - float64(prev.CPUUsage.TotalUsage)
	systemDelta := float64(cur.SystemUsage) - float64(prev.SystemUsage)
	if cpuDelta <= 0 || systemDelta <= 0 {
		return 0
	}
	cpus := float64(cur.OnlineCPUs)
	if cpus == 0 {
		cpus = float64(len(cur.CPUUsage.PercpuUsage))
	}
	if cpus == 0 {
		cpus = 1
	}
	return cpuDelta / systemDelta * cpus * 100
}

// memoryUsage subtracts reclaimable page cache from the raw usage:
// "inactive_file" on cgroup v2, "total_inactive_file" on cgroup v1.
func memoryUsage(m container.MemoryStats) uint64 {
	cache, ok := m.Stats["inactive_file"]
	if !ok {
		cache = m.Stats["total_inactive_file"]
	}
	if cache < m.Usage {
		return m.Usage - cache
	}
	return m.Usage
}
