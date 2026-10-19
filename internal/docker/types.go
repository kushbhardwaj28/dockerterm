// Package docker talks to the Docker Engine API and converts its responses
// into the small, UI-friendly types used by the rest of dockerterm.
package docker

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Container is a summary of one container, as shown in the container list.
type Container struct {
	ID       string
	Name     string
	Image    string
	Command  string
	State    string // machine state: running, exited, paused, created, restarting, dead
	Status   string // human status, e.g. "Up 3 days (healthy)"
	Health   string // healthy, unhealthy, starting, or empty
	Created  time.Time
	Ports    []Port
	Networks []string
	Labels   map[string]string
}

// Running reports whether the container is running (paused counts as not running).
func (c Container) Running() bool { return c.State == "running" }

// ShortID returns the first 12 characters of the container ID, like the docker CLI.
func (c Container) ShortID() string {
	if len(c.ID) > 12 {
		return c.ID[:12]
	}
	return c.ID
}

// ComposeProject returns the compose project the container belongs to, if any.
func (c Container) ComposeProject() string {
	return c.Labels["com.docker.compose.project"]
}

// Port is a port exposed by a container.
type Port struct {
	IP          string
	PrivatePort uint16
	PublicPort  uint16
	Type        string
}

func (p Port) String() string {
	if p.PublicPort == 0 {
		return fmt.Sprintf("%d/%s", p.PrivatePort, p.Type)
	}
	ip := p.IP
	if strings.Contains(ip, ":") {
		ip = "[" + ip + "]"
	}
	if ip != "" {
		ip += ":"
	}
	return fmt.Sprintf("%s%d->%d/%s", ip, p.PublicPort, p.PrivatePort, p.Type)
}

// FormatPorts renders ports the way `docker ps` does, sorted and with the
// IPv6 twin of an IPv4 binding dropped to save space.
func FormatPorts(ports []Port) string {
	sorted := slices.Clone(ports)
	slices.SortFunc(sorted, func(a, b Port) int {
		if a.PrivatePort != b.PrivatePort {
			return int(a.PrivatePort) - int(b.PrivatePort)
		}
		if a.Type != b.Type {
			return strings.Compare(a.Type, b.Type)
		}
		return strings.Compare(a.IP, b.IP)
	})
	seen := map[string]bool{}
	out := make([]string, 0, len(sorted))
	for _, p := range sorted {
		key := fmt.Sprintf("%d/%d/%s", p.PublicPort, p.PrivatePort, p.Type)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, p.String())
	}
	return strings.Join(out, ", ")
}

// Stats is a point-in-time resource usage sample for one container.
type Stats struct {
	CPUPercent  float64
	MemoryUsage uint64 // bytes, excluding page cache (matches `docker stats`)
	MemoryLimit uint64 // bytes
	PIDs        uint64
	NetRx       uint64 // bytes received, all interfaces
	NetTx       uint64 // bytes sent, all interfaces
}

// MemoryPercent is memory usage as a percentage of the limit.
func (s Stats) MemoryPercent() float64 {
	if s.MemoryLimit == 0 {
		return 0
	}
	return float64(s.MemoryUsage) / float64(s.MemoryLimit) * 100
}
