package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// actionTimeout is generous: stop waits for the container's grace period.
const actionTimeout = 60 * time.Second

// run executes a daemon call off the UI goroutine and reports the outcome.
func (m Model) run(done string, fn func(ctx context.Context) error) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		return actionMsg{done: done, err: fn(ctx)}
	}
}

func (m *Model) action(msg tea.KeyPressMsg) tea.Cmd {
	if m.view != viewContainers {
		if key.Matches(msg, m.keys.Remove) {
			return m.removeResource()
		}
		return nil
	}
	c := m.selectedContainer()
	if c == nil {
		return nil
	}
	b, k, id, name := m.backend, m.keys, c.ID, c.Name
	switch {
	case key.Matches(msg, k.Start):
		if c.State == "running" {
			m.setToast(name+" is already running", toastWarn)
			return nil
		}
		m.setToast("starting "+name+"…", toastInfo)
		return m.run("started "+name, func(ctx context.Context) error { return b.StartContainer(ctx, id) })

	case key.Matches(msg, k.Stop):
		if c.State != "running" && c.State != "paused" && c.State != "restarting" {
			m.setToast(name+" is not running", toastWarn)
			return nil
		}
		m.setToast("stopping "+name+"…", toastInfo)
		return m.run("stopped "+name, func(ctx context.Context) error { return b.StopContainer(ctx, id) })

	case key.Matches(msg, k.Restart):
		m.setToast("restarting "+name+"…", toastInfo)
		return m.run("restarted "+name, func(ctx context.Context) error { return b.RestartContainer(ctx, id) })

	case key.Matches(msg, k.Pause):
		switch c.State {
		case "paused":
			return m.run("unpaused "+name, func(ctx context.Context) error { return b.UnpauseContainer(ctx, id) })
		case "running":
			return m.run("paused "+name, func(ctx context.Context) error { return b.PauseContainer(ctx, id) })
		}
		m.setToast("only a running container can be paused", toastWarn)
		return nil

	case key.Matches(msg, k.Remove):
		force := c.State == "running" || c.State == "paused"
		body := "The container will be deleted. Named volumes are kept."
		cmd := "docker rm " + name
		if force {
			body = "It is running — it will be force-stopped and removed. Named volumes are kept."
			cmd = "docker rm -f " + name
		}
		m.confirm = &confirmState{
			title: "Remove container " + name + "?", body: body, cmd: cmd, verb: "remove",
			run: m.run("removed "+name, func(ctx context.Context) error { return b.RemoveContainer(ctx, id, force) }),
		}
		return nil

	case key.Matches(msg, k.Exec):
		if c.State != "running" {
			m.setToast("exec needs a running container", toastWarn)
			return nil
		}
		return m.execShell(id, name)
	}
	return nil
}

// execShell suspends the UI and opens an interactive shell with the docker CLI,
// which handles the terminal (raw mode, resizing) for us.
func (m *Model) execShell(id, name string) tea.Cmd {
	bin, err := exec.LookPath("docker")
	if err != nil {
		m.setToast("exec needs the docker CLI on your PATH", toastErr)
		return nil
	}
	cmd := exec.Command(bin, "exec", "-it", id, "sh", "-c",
		"if command -v bash >/dev/null 2>&1; then exec bash; else exec sh; fi")
	if m.opts.DockerHost != "" {
		cmd.Env = append(os.Environ(), "DOCKER_HOST="+m.opts.DockerHost)
	}
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		var exit *exec.ExitError
		if errors.As(err, &exit) && (exit.ExitCode() == 126 || exit.ExitCode() == 127) {
			return actionMsg{err: fmt.Errorf("exec %s: exit %d — the image may not include sh or bash", name, exit.ExitCode())}
		}
		if err != nil {
			return actionMsg{err: fmt.Errorf("exec %s: %w", name, err)}
		}
		return actionMsg{done: "left shell in " + name}
	})
}

func (m *Model) removeResource() tea.Cmd {
	b := m.backend
	switch m.view {
	case viewImages:
		img := m.selectedImage()
		if img == nil {
			return nil
		}
		ref := img.Ref()
		body := ref + " will be untagged, and deleted if no other tag uses it."
		if users := m.containersUsingImage(img.ID); len(users) > 0 {
			body = fmt.Sprintf("%s is used by %d container(s); docker will refuse unless they are removed first.", ref, len(users))
		}
		m.confirm = &confirmState{
			title: "Remove image?", body: body, cmd: "docker rmi " + ref, verb: "remove",
			run: m.run("removed "+ref, func(ctx context.Context) error { return b.RemoveImage(ctx, ref) }),
		}
	case viewVolumes:
		v := m.selectedVolume()
		if v == nil {
			return nil
		}
		name := v.Name
		m.confirm = &confirmState{
			title: "Remove volume?", body: name + " and all data in it will be deleted.", cmd: "docker volume rm " + name, verb: "remove",
			run: m.run("removed volume "+name, func(ctx context.Context) error { return b.RemoveVolume(ctx, name) }),
		}
	case viewNetworks:
		n := m.selectedNetwork()
		if n == nil {
			return nil
		}
		if n.Builtin() {
			m.setToast("the "+n.Name+" network is built in and cannot be removed", toastWarn)
			return nil
		}
		id, name := n.ID, n.Name
		m.confirm = &confirmState{
			title: "Remove network?", body: name + " will be deleted.", cmd: "docker network rm " + name, verb: "remove",
			run: m.run("removed network "+name, func(ctx context.Context) error { return b.RemoveNetwork(ctx, id) }),
		}
	}
	return nil
}
