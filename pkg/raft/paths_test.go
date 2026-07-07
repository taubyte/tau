package raft

import (
	"path/filepath"
	"testing"
)

func TestSnapshotDir(t *testing.T) {
	got := SnapshotDir("/var/tau", "test", "main")
	want := filepath.Join("/var/tau", "raft", "test", "main")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}

	got = SnapshotDir("/r", "prod", "team/west")
	want = filepath.Join("/r", "raft", "prod", "team-west")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}

	got = SnapshotDir("/r", "prod", "")
	want = filepath.Join("/r", "raft", "prod", "main")
	if got != want {
		t.Fatalf("empty namespace: got %q want %q", got, want)
	}
}
