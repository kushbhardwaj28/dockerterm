package docker_cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os/exec"
	"time"
)

const commandTimeout = 5 * time.Second

func decodeJSONLines[T any](reader io.Reader) ([]T, error) {
	decoder := json.NewDecoder(reader)
	var items []T
	for {
		var item T
		if err := decoder.Decode(&item); err != nil {
			if err == io.EOF {
				return items, nil
			}
			return nil, err
		}
		items = append(items, item)
	}
}

func ListContainers(ctx context.Context) ([]DockerPs, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, "docker", "ps", "-a", "--no-trunc", "--format", "json").Output()
	if err != nil {
		return nil, fmt.Errorf("docker ps: %w", err)
	}
	return decodeJSONLines[DockerPs](bytes.NewReader(output))
}

func SnapshotStats(ctx context.Context) ([]DockerStats, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, "docker", "stats", "--no-stream", "--no-trunc", "--format", "{{json .}}").Output()
	if err != nil {
		return nil, fmt.Errorf("docker stats: %w", err)
	}
	return decodeJSONLines[DockerStats](bytes.NewReader(output))
}

// Docker commands

type DockerCmd int

type alpha int

const (
	DockerPS DockerCmd = iota
)

var DockerCmdName = map[DockerCmd]string{
	DockerPS: "dockerps",
}

func (cmd DockerCmd) String() string {
	return DockerCmdName[cmd]
}

func RunDockerCmd(cmd DockerCmd) []DockerPs {
	switch cmd {
	case DockerPS:
		raw_response := []DockerPs{}
		response := exec.Command("docker", "ps", "-a", "--format", "json")
		output_bytes, err := response.Output()
		log.Printf("docker ps output: %s", output_bytes)
		if err != nil {
			log.Printf("docker ps error: %v", err)
			return raw_response
		}

		reader := bytes.NewReader(output_bytes)
		decoder := json.NewDecoder(reader)

		var response_data []DockerPs

		for decoder.More() {
			var dps DockerPs
			if err := decoder.Decode(&dps); err != nil {
				log.Fatalf("Error decoding JSON line: %v", err)
			}
			response_data = append(response_data, dps)
		}

		return response_data
	default:
		return []DockerPs{}
	}
}
