package raft

import (
	"path/filepath"
	"testing"
)

func TestSnapshotDir(t *testing.T) {
	got := SnapshotDir("/var/tau", "test", "main")
	want := filepath.Join("/var/tau", "storage", "test", "raft-snapshots", "main")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}

	got = SnapshotDir("/r", "prod", "team/west")
	want = filepath.Join("/r", "storage", "prod", "raft-snapshots", "team-west")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}

	got = SnapshotDir("/r", "prod", "")
	want = filepath.Join("/r", "storage", "prod", "raft-snapshots", "main")
	if got != want {
		t.Fatalf("empty namespace: got %q want %q", got, want)
	}
}
