package docker

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/volume"
)

// Info describes the daemon and its host.
type Info struct {
	Version  string
	OS       string
	NCPU     int
	MemTotal int64
}

// Image is one image, or one tag of it.
type Image struct {
	ID      string // full sha256:… id
	Repo    string
	Tag     string
	Size    int64
	Created time.Time
}

// ShortID is the 12-character id shown by `docker images`.
func (i Image) ShortID() string {
	id := strings.TrimPrefix(i.ID, "sha256:")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// Dangling reports whether the image has no tag.
func (i Image) Dangling() bool { return i.Repo == "<none>" }

// Ref is repo:tag, or the short id for dangling images.
func (i Image) Ref() string {
	if i.Dangling() {
		return i.ShortID()
	}
	return i.Repo + ":" + i.Tag
}

// Volume is a named volume.
type Volume struct {
	Name       string
	Driver     string
	Mountpoint string
	Scope      string
	Created    time.Time
	Size       int64 // -1 when unknown (only disk usage reports it)
}

// Network is a docker network.
type Network struct {
	ID         string
	Name       string
	Driver     string
	Scope      string
	Subnet     string
	Internal   bool
	Attachable bool
	Created    time.Time
}

// Builtin reports whether docker created the network itself.
func (n Network) Builtin() bool {
	return n.Name == "bridge" || n.Name == "host" || n.Name == "none"
}

// DiskUsage is the space docker uses, as `docker system df` reports it.
type DiskUsage struct {
	Images      int64
	Containers  int64
	Volumes     int64
	BuildCache  int64
	VolumeSizes map[string]int64
}

// ContainerDetail is what `docker inspect` adds to the summary.
type ContainerDetail struct {
	ID            string
	Env           []string
	Mounts        []Mount
	Entrypoint    []string
	Cmd           []string
	WorkingDir    string
	User          string
	RestartPolicy string
	RestartCount  int
	IPs           map[string]string // network name → IP
	Tty           bool
	Raw           string // indented inspect JSON
}

// Mount is a bind mount, volume or tmpfs inside a container.
type Mount struct {
	Type        string
	Name        string
	Source      string
	Destination string
	RW          bool
}

// LogLine is one line of container output.
type LogLine struct {
	Time   time.Time
	Stream string // "stdout" or "stderr"
	Text   string
}

func imagesFromSummary(s image.Summary) []Image {
	base := Image{ID: s.ID, Size: s.Size, Created: time.Unix(s.Created, 0)}
	if len(s.RepoTags) == 0 {
		base.Repo, base.Tag = "<none>", "<none>"
		return []Image{base}
	}
	out := make([]Image, 0, len(s.RepoTags))
	for _, rt := range s.RepoTags {
		img := base
		img.Repo, img.Tag = splitRepoTag(rt)
		out = append(out, img)
	}
	return out
}

// splitRepoTag splits "registry:5000/app:1.2" into "registry:5000/app" and "1.2".
func splitRepoTag(ref string) (string, string) {
	i := strings.LastIndex(ref, ":")
	if i < 0 || strings.Contains(ref[i:], "/") {
		return ref, "latest"
	}
	return ref[:i], ref[i+1:]
}

func volumeFromAPI(v volume.Volume) Volume {
	out := Volume{Name: v.Name, Driver: v.Driver, Mountpoint: v.Mountpoint, Scope: v.Scope, Size: -1}
	if t, err := time.Parse(time.RFC3339, v.CreatedAt); err == nil {
		out.Created = t
	}
	if v.UsageData != nil && v.UsageData.Size >= 0 {
		out.Size = v.UsageData.Size
	}
	return out
}

func networkFromSummary(n network.Summary) Network {
	out := Network{
		ID: n.ID, Name: n.Name, Driver: n.Driver, Scope: n.Scope,
		Internal: n.Internal, Attachable: n.Attachable, Created: n.Created,
	}
	var subnets []string
	for _, c := range n.IPAM.Config {
		if c.Subnet.IsValid() {
			subnets = append(subnets, c.Subnet.String())
		}
	}
	out.Subnet = strings.Join(subnets, ", ")
	return out
}

func detailFromInspect(r container.InspectResponse, raw json.RawMessage) ContainerDetail {
	d := ContainerDetail{ID: r.ID, RestartCount: r.RestartCount, IPs: map[string]string{}}
	if r.Config != nil {
		d.Env = r.Config.Env
		d.Entrypoint = r.Config.Entrypoint
		d.Cmd = r.Config.Cmd
		d.WorkingDir = r.Config.WorkingDir
		d.User = r.Config.User
		d.Tty = r.Config.Tty
	}
	if r.HostConfig != nil {
		d.RestartPolicy = string(r.HostConfig.RestartPolicy.Name)
	}
	for _, m := range r.Mounts {
		d.Mounts = append(d.Mounts, Mount{Type: string(m.Type), Name: m.Name, Source: m.Source, Destination: m.Destination, RW: m.RW})
	}
	if r.NetworkSettings != nil {
		for name, ep := range r.NetworkSettings.Networks {
			if ep != nil && ep.IPAddress.IsValid() {
				d.IPs[name] = ep.IPAddress.String()
			}
		}
	}
	var buf bytes.Buffer
	if json.Indent(&buf, raw, "", "  ") == nil {
		d.Raw = buf.String()
	}
	return d
}
