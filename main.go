// Command dockerterm is a terminal UI for Docker.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/kushbhardwaj28/dockerterm/internal/docker"
	"github.com/kushbhardwaj28/dockerterm/internal/ui"
)

// version is set at build time: go build -ldflags "-X main.version=v0.1.0"
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "dockerterm:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		host        = flag.String("host", "", "daemon socket to connect to (default: DOCKER_HOST, then the current docker context)")
		interval    = flag.Duration("interval", 2*time.Second, "how often to refresh")
		debug       = flag.Bool("debug", os.Getenv("DOCKERTERM_DEBUG") != "", "write a debug log to "+debugLogPath())
		showVersion = flag.Bool("version", false, "print the version and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println("dockerterm", version)
		return nil
	}
	if *debug {
		f, err := tea.LogToFile(debugLogPath(), "debug")
		if err != nil {
			return fmt.Errorf("debug log: %w", err)
		}
		defer f.Close()
	}

	endpoint, err := docker.ResolveEndpoint(*host)
	if err != nil {
		return err
	}
	client, err := docker.NewClient(endpoint)
	if err != nil {
		return err
	}
	defer client.Close()

	model := ui.New(client, ui.Options{Endpoint: endpoint.Label(), DockerHost: endpoint.Host, RefreshInterval: *interval})
	_, err = tea.NewProgram(model).Run()
	return err
}

func debugLogPath() string {
	return filepath.Join(os.TempDir(), "dockerterm-debug.log")
}
