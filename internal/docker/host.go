package docker

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Endpoint says which daemon to talk to and where that choice came from.
type Endpoint struct {
	Host    string // e.g. unix:///var/run/docker.sock; empty means the client default
	Context string // docker context name, when the host came from a context
	Source  string // "flag", "DOCKER_HOST", "context" or "default"
}

// Label is a short description for the header bar.
func (e Endpoint) Label() string {
	host := e.Host
	if host == "" {
		host = "default socket"
	}
	if e.Context != "" {
		return e.Context + " · " + host
	}
	return host
}

// ResolveEndpoint picks the daemon the same way the docker CLI does:
// an explicit host, then DOCKER_HOST, then DOCKER_CONTEXT or the current
// context in the docker config, then the platform default.
func ResolveEndpoint(flagHost string) (Endpoint, error) {
	return resolveEndpoint(flagHost, os.Getenv, configDir())
}

func resolveEndpoint(flagHost string, getenv func(string) string, cfgDir string) (Endpoint, error) {
	if flagHost != "" {
		return Endpoint{Host: flagHost, Source: "flag"}, nil
	}
	if h := getenv("DOCKER_HOST"); h != "" {
		return Endpoint{Host: h, Source: "DOCKER_HOST"}, nil
	}
	name := getenv("DOCKER_CONTEXT")
	if name == "" {
		var err error
		if name, err = currentContext(cfgDir); err != nil {
			return Endpoint{}, err
		}
	}
	if name == "" || name == "default" {
		return Endpoint{Source: "default"}, nil
	}
	host, err := contextHost(cfgDir, name)
	if err != nil {
		return Endpoint{}, err
	}
	return Endpoint{Host: host, Context: name, Source: "context"}, nil
}

func configDir() string {
	if dir := os.Getenv("DOCKER_CONFIG"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".docker")
}

func currentContext(cfgDir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(cfgDir, "config.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read docker config: %w", err)
	}
	var cfg struct {
		CurrentContext string `json:"currentContext"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return "", fmt.Errorf("parse docker config: %w", err)
	}
	return cfg.CurrentContext, nil
}

// contextHost reads the docker endpoint of a context. Context metadata lives in
// contexts/meta/<sha256 of the name>/meta.json under the docker config dir.
func contextHost(cfgDir, name string) (string, error) {
	sum := sha256.Sum256([]byte(name))
	path := filepath.Join(cfgDir, "contexts", "meta", hex.EncodeToString(sum[:]), "meta.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("docker context %q: %w", name, err)
	}
	var meta struct {
		Endpoints map[string]struct {
			Host string `json:"Host"`
		} `json:"Endpoints"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return "", fmt.Errorf("docker context %q: %w", name, err)
	}
	host := meta.Endpoints["docker"].Host
	if host == "" {
		return "", fmt.Errorf("docker context %q has no docker endpoint", name)
	}
	return host, nil
}
