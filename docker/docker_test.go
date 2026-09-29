package docker_cmd

import (
	"context"
	"strings"
	"testing"
)

func TestDecodeStatsLines(t *testing.T) {
	input := `{"ID":"aaa","CPUPerc":"3.40%","MemUsage":"34MiB / 1GiB"}
{"ID":"bbb","CPUPerc":"0.00%","MemUsage":"12MiB / 1GiB"}`
	got, err := decodeJSONLines[DockerStats](strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "aaa" || got[0].CPUPerc != "3.40%" || got[1].MemUsage != "12MiB / 1GiB" {
		t.Fatalf("decoded stats = %+v", got)
	}
	if _, err := decodeJSONLines[DockerStats](strings.NewReader(`{"ID":"aaa"}
not json`)); err == nil {
		t.Fatal("malformed JSON line must return an error")
	}
}

func TestEmptySnapshotAndCanceledDockerCommand(t *testing.T) {
	items, err := decodeJSONLines[DockerStats](strings.NewReader("\n"))
	if err != nil || len(items) != 0 {
		t.Fatalf("empty snapshot = %v, %v; want zero items and no error", items, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := SnapshotStats(ctx); err == nil {
		t.Fatal("canceled stats command must return an error")
	}
}
