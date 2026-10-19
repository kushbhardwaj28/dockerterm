# dockerterm

A terminal UI for Docker: see your containers, their status and live resource usage, and (soon) manage them without leaving the terminal.

![dockerterm design](docs/screenshot.png)

> Early work in progress. Today dockerterm lists containers with live CPU and memory and shows details for the selected one. Start/stop/restart, logs, exec, images, volumes and networks are next.

## Install

Requires Go 1.25 or newer.

```sh
go install github.com/kushbhardwaj28/dockerterm@latest
```

Or build from a clone:

```sh
make build   # produces ./dockerterm
```

## Usage

```sh
dockerterm                     # uses DOCKER_HOST, else your current docker context
dockerterm -host unix:///var/run/docker.sock
dockerterm -interval 5s        # refresh less often
dockerterm -debug              # writes a debug log to $TMPDIR/dockerterm-debug.log
```

| Key | Action |
| --- | --- |
| `↑` `↓` / `j` `k` | move |
| `g` / `G` | first / last |
| `enter` | toggle details |
| `ctrl+r` | refresh now |
| `q` / `ctrl+c` | quit |

### Which daemon it connects to

The same order the docker CLI uses: `-host`, then `DOCKER_HOST`, then `DOCKER_CONTEXT` or the current context in `~/.docker/config.json` (so Docker Desktop, Colima and OrbStack contexts work), then the platform default socket. `DOCKER_TLS_VERIFY`, `DOCKER_CERT_PATH` and `DOCKER_API_VERSION` are honoured. `ssh://` hosts are not supported yet.

## Development

```sh
make test    # go test -race ./...
make lint    # golangci-lint run
make run     # go run . -debug
```

Layout:

```
main.go             flags, debug log, wiring
internal/docker     Docker Engine API client (github.com/moby/moby/client) and UI-friendly types
internal/ui         Bubble Tea model, layout, table and key bindings
```

The UI depends on a small `ui.Backend` interface rather than the Docker client, so the model is tested with a fake backend and the Docker package is tested against a fake Engine API server.
