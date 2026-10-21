package docker

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/network"
)

func TestContainerFromSummary(t *testing.T) {
	got := containerFromSummary(container.Summary{
		ID:      "a81be44c09f2deadbeef",
		Names:   []string{"/api-server"},
		Image:   "myorg/api:2.4.1",
		State:   container.StateRunning,
		Status:  "Up 3 days (healthy)",
		Created: 1758700000,
		Health:  &container.HealthSummary{Status: container.Healthy},
		Labels:  map[string]string{"com.docker.compose.project": "app"},
		Ports: []container.PortSummary{
			{IP: netip.MustParseAddr("0.0.0.0"), PrivatePort: 8080, PublicPort: 8080, Type: "tcp"},
			{IP: netip.MustParseAddr("::"), PrivatePort: 8080, PublicPort: 8080, Type: "tcp"},
			{PrivatePort: 9229, Type: "tcp"},
		},
		NetworkSettings: &container.NetworkSettingsSummary{Networks: map[string]*network.EndpointSettings{
			"app_backend": {}, "app_frontend": {},
		}},
	})
	if got.Name != "api-server" || !got.Running() || got.Health != "healthy" || got.ShortID() != "a81be44c09f2" {
		t.Errorf("converted container = %+v", got)
	}
	if got.ComposeProject() != "app" {
		t.Errorf("compose project = %q", got.ComposeProject())
	}
	if want := "app_backend,app_frontend"; strings.Join(got.Networks, ",") != want {
		t.Errorf("networks = %v, want %s", got.Networks, want)
	}
	if want := "0.0.0.0:8080->8080/tcp, 9229/tcp"; FormatPorts(got.Ports) != want {
		t.Errorf("ports = %q, want %q", FormatPorts(got.Ports), want)
	}
}

func TestPortStringIPv6(t *testing.T) {
	if got := (Port{IP: "::1", PrivatePort: 80, PublicPort: 8080, Type: "tcp"}).String(); got != "[::1]:8080->80/tcp" {
		t.Errorf("ipv6 port = %q", got)
	}
}

func TestStatsFromResponse(t *testing.T) {
	r := container.StatsResponse{
		PreCPUStats: container.CPUStats{CPUUsage: container.CPUUsage{TotalUsage: 1_000}, SystemUsage: 10_000},
		CPUStats:    container.CPUStats{CPUUsage: container.CPUUsage{TotalUsage: 1_500}, SystemUsage: 20_000, OnlineCPUs: 4},
		MemoryStats: container.MemoryStats{Usage: 100 << 20, Limit: 1 << 30, Stats: map[string]uint64{"inactive_file": 30 << 20}},
		PidsStats:   container.PidsStats{Current: 12},
		Networks: map[string]container.NetworkStats{
			"eth0": {RxBytes: 100, TxBytes: 50},
			"eth1": {RxBytes: 1, TxBytes: 2},
		},
	}
	s := statsFromResponse(r)
	// 500/10000 * 4 cpus * 100 = 20%
	if math.Abs(s.CPUPercent-20) > 1e-9 {
		t.Errorf("cpu = %v, want 20", s.CPUPercent)
	}
	if s.MemoryUsage != 70<<20 || s.MemoryLimit != 1<<30 || s.PIDs != 12 || s.NetRx != 101 || s.NetTx != 52 {
		t.Errorf("stats = %+v", s)
	}
	if math.Abs(s.MemoryPercent()-70.0/1024*100) > 1e-9 {
		t.Errorf("mem%% = %v", s.MemoryPercent())
	}
}

func TestStatsCgroupV1CacheAndNoPreviousSample(t *testing.T) {
	r := container.StatsResponse{
		CPUStats:    container.CPUStats{CPUUsage: container.CPUUsage{TotalUsage: 500, PercpuUsage: []uint64{1, 2}}, SystemUsage: 1000},
		MemoryStats: container.MemoryStats{Usage: 50, Stats: map[string]uint64{"total_inactive_file": 20}},
	}
	s := statsFromResponse(r)
	if s.CPUPercent != 100 { // 500/1000 * 2 per-cpu entries * 100
		t.Errorf("cpu = %v, want 100", s.CPUPercent)
	}
	if s.MemoryUsage != 30 || s.MemoryPercent() != 0 {
		t.Errorf("mem = %d (%v%%)", s.MemoryUsage, s.MemoryPercent())
	}
	if got := statsFromResponse(container.StatsResponse{}); got.CPUPercent != 0 {
		t.Errorf("empty sample cpu = %v", got.CPUPercent)
	}
}

func TestResolveEndpoint(t *testing.T) {
	dir := t.TempDir()
	writeContext(t, dir, "colima", "unix:///home/me/.colima/docker.sock")
	env := map[string]string{}
	getenv := func(k string) string { return env[k] }

	if ep, _ := resolveEndpoint("", getenv, dir); ep.Source != "default" || ep.Host != "" {
		t.Errorf("no config = %+v, want default", ep)
	}
	writeFile(t, filepath.Join(dir, "config.json"), `{"currentContext":"colima"}`)
	if ep, err := resolveEndpoint("", getenv, dir); err != nil || ep.Host != "unix:///home/me/.colima/docker.sock" || ep.Context != "colima" {
		t.Errorf("current context = %+v, %v", ep, err)
	}
	env["DOCKER_CONTEXT"] = "missing"
	if _, err := resolveEndpoint("", getenv, dir); err == nil {
		t.Error("unknown DOCKER_CONTEXT must be an error")
	}
	env["DOCKER_HOST"] = "tcp://10.0.0.5:2375"
	if ep, _ := resolveEndpoint("", getenv, dir); ep.Host != "tcp://10.0.0.5:2375" || ep.Source != "DOCKER_HOST" {
		t.Errorf("DOCKER_HOST = %+v", ep)
	}
	if ep, _ := resolveEndpoint("unix:///tmp/d.sock", getenv, dir); ep.Host != "unix:///tmp/d.sock" || ep.Source != "flag" {
		t.Errorf("flag = %+v", ep)
	}
}

func writeContext(t *testing.T, cfgDir, name, host string) {
	t.Helper()
	sum := sha256.Sum256([]byte(name))
	meta, _ := json.Marshal(map[string]any{"Name": name, "Endpoints": map[string]any{"docker": map[string]string{"Host": host}}})
	writeFile(t, filepath.Join(cfgDir, "contexts", "meta", hex.EncodeToString(sum[:]), "meta.json"), string(meta))
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// muxFrame encodes one chunk of the multiplexed log stream.
func muxFrame(stream stdcopy.StdType, payload string) []byte {
	head := []byte{byte(stream), 0, 0, 0, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(head[4:], uint32(len(payload)))
	return append(head, payload...)
}

// fakeDaemon serves just enough of the Engine API for the client tests.
func fakeDaemon(t *testing.T) *Client {
	t.Helper()
	version := regexp.MustCompile(`^/v[0-9.]+`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Api-Version", "1.51")
		w.Header().Set("Content-Type", "application/json")
		switch path := version.ReplaceAllString(r.URL.Path, ""); {
		case path == "/_ping":
			_, _ = w.Write([]byte("OK"))
		case path == "/containers/json":
			if r.URL.Query().Get("all") != "1" {
				t.Errorf("list must ask for all containers, query = %s", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode([]container.Summary{
				{ID: "aaa", Names: []string{"/web"}, State: container.StateRunning},
				{ID: "bbb", Names: []string{"/old"}, State: container.StateExited},
			})
		case path == "/containers/aaa/start" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusNoContent)
		case path == "/containers/aaa/json":
			_ = json.NewEncoder(w).Encode(container.InspectResponse{ID: "aaa", Config: &container.Config{Tty: false, Env: []string{"A=1"}}})
		case path == "/containers/aaa/logs":
			w.Header().Set("Content-Type", "application/vnd.docker.multiplexed-stream")
			_, _ = w.Write(muxFrame(stdcopy.Stdout, "2026-10-20T10:41:02.000000000Z hello\n"))
			_, _ = w.Write(muxFrame(stdcopy.Stderr, "2026-10-20T10:41:03.000000000Z oops\n"))
		case path == "/containers/aaa/stats":
			if r.URL.Query().Get("stream") != "true" {
				t.Error("stats must be streamed")
			}
			// like the daemon: one sample per tick until the client goes away
			for i := uint64(1); ; i++ {
				_ = json.NewEncoder(w).Encode(container.StatsResponse{
					PreCPUStats: container.CPUStats{CPUUsage: container.CPUUsage{TotalUsage: (i - 1) * 10}, SystemUsage: (i - 1) * 100},
					CPUStats:    container.CPUStats{CPUUsage: container.CPUUsage{TotalUsage: i * 10}, SystemUsage: i * 100, OnlineCPUs: 1},
					MemoryStats: container.MemoryStats{Usage: 2048, Limit: 4096},
				})
				w.(http.Flusher).Flush()
				select {
				case <-r.Context().Done():
					return
				case <-time.After(20 * time.Millisecond):
				}
			}
		case path == "/containers/slow/stats":
			// a busy daemon that takes ages to send the first sample
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
		default:
			http.Error(w, `{"message":"no such container"}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	c, err := NewClient(Endpoint{Host: "tcp://" + strings.TrimPrefix(srv.URL, "http://")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestClientListAndStats(t *testing.T) {
	c := fakeDaemon(t)
	ctx := context.Background()
	list, err := c.ListContainers(ctx)
	if err != nil || len(list) != 2 || list[0].Name != "web" || list[1].State != "exited" {
		t.Fatalf("list = %+v, %v", list, err)
	}
	var stats map[string]Stats
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		stats, err = c.Stats(ctx, []string{"aaa", "zzz"})
		if _, ok := stats["aaa"]; ok && err != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err == nil || !strings.Contains(err.Error(), "zzz") {
		t.Errorf("stats error = %v, want the failing container named", err)
	}
	if s, ok := stats["aaa"]; !ok || s.CPUPercent != 10 || s.MemoryUsage != 2048 {
		t.Errorf("stats = %+v, want the streamed sample", stats)
	}

	// containers that are no longer asked for stop streaming
	if _, err := c.Stats(ctx, nil); err != nil {
		t.Errorf("empty stats: %v", err)
	}
	c.stats.mu.Lock()
	open := len(c.stats.streams)
	c.stats.mu.Unlock()
	if open != 0 {
		t.Errorf("%d streams still open", open)
	}
}

// Regression: a slow daemon used to make every refresh hit its deadline
// ("context deadline exceeded"). Stats must never wait on the daemon.
func TestStatsDoNotBlockOnSlowDaemon(t *testing.T) {
	c := fakeDaemon(t)
	start := time.Now()
	stats, err := c.Stats(context.Background(), []string{"slow"})
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Errorf("Stats took %v, want it to return at once", elapsed)
	}
	if err != nil || len(stats) != 0 {
		t.Errorf("first call = %v, %v; want no sample yet and no error", stats, err)
	}
}

func TestClientActionsInspectAndLogs(t *testing.T) {
	c := fakeDaemon(t)
	ctx := context.Background()
	if err := c.StartContainer(ctx, "aaa"); err != nil {
		t.Errorf("start: %v", err)
	}
	if err := c.StopContainer(ctx, "zzz"); err == nil {
		t.Error("stop of unknown container must fail")
	}
	d, err := c.InspectContainer(ctx, "aaa")
	if err != nil || d.ID != "aaa" || d.Env[0] != "A=1" || !strings.Contains(d.Raw, "\n  ") {
		t.Errorf("inspect = %+v, %v", d, err)
	}
	logs, err := c.Logs(ctx, "aaa", 10)
	if err != nil || len(logs) != 2 || logs[0].Text != "hello" || logs[1].Stream != "stderr" || logs[1].Time.Second() != 3 {
		t.Errorf("logs = %+v, %v", logs, err)
	}
}

func TestImagesAndNetworks(t *testing.T) {
	for in, want := range map[string][2]string{
		"nginx:1.27":                   {"nginx", "1.27"},
		"registry:5000/team/app:2":     {"registry:5000/team/app", "2"},
		"registry:5000/team/app":       {"registry:5000/team/app", "latest"},
		"ghcr.io/org/tool@sha256:abcd": {"ghcr.io/org/tool@sha256", "abcd"},
	} {
		if r, tag := splitRepoTag(in); r != want[0] || tag != want[1] {
			t.Errorf("splitRepoTag(%q) = %q, %q", in, r, tag)
		}
	}
	imgs := imagesFromSummary(image.Summary{ID: "sha256:0123456789abcdef", RepoTags: []string{"a:1", "b:2"}, Size: 10})
	if len(imgs) != 2 || imgs[1].Ref() != "b:2" || imgs[0].ShortID() != "0123456789ab" {
		t.Errorf("images = %+v", imgs)
	}
	dangling := imagesFromSummary(image.Summary{ID: "sha256:fedcba9876543210"})
	if !dangling[0].Dangling() || dangling[0].Ref() != "fedcba987654" {
		t.Errorf("dangling = %+v", dangling)
	}
	n := networkFromSummary(network.Summary{Network: network.Network{Name: "app", IPAM: network.IPAM{Config: []network.IPAMConfig{{Subnet: netip.MustParsePrefix("172.20.0.0/16")}}}}})
	if n.Subnet != "172.20.0.0/16" || n.Builtin() || !(Network{Name: "bridge"}).Builtin() {
		t.Errorf("network = %+v", n)
	}
}

func TestNewClientRejectsSSH(t *testing.T) {
	if _, err := NewClient(Endpoint{Host: "ssh://me@box"}); err == nil {
		t.Error("ssh host must be rejected with a clear error")
	}
}
