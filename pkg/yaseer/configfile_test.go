package seer

import (
	"os"
	"path/filepath"
	"testing"
)

// TestReadConfigYaml — pin yaseer's behaviour around a single
// top-level `config.yaml` file (the AgentViewer config layout). If
// yaseer can't read it, this test will say where it falls down so
// we can patch.
func TestReadConfigYaml(t *testing.T) {
	dir := t.TempDir()
	body := []byte(`
node:
  peer_id: 12D3KooFakePeerID
  listen_port: 4001
theme:
  variant: dark
  accent: "#E3893F"
`)
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), body, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	s, err := New(SystemFS(dir))
	if err != nil {
		t.Fatalf("seer.New: %v", err)
	}

	t.Run("Document() at root config", func(t *testing.T) {
		var peerID string
		err := s.Get("config").Document().Get("node").Get("peer_id").Value(&peerID)
		if err != nil {
			t.Fatalf("read peer_id: %v", err)
		}
		if peerID != "12D3KooFakePeerID" {
			t.Errorf("peer_id = %q, want 12D3KooFakePeerID", peerID)
		}
	})

	t.Run("nested Get of int", func(t *testing.T) {
		var port int
		if err := s.Get("config").Document().Get("node").Get("listen_port").Value(&port); err != nil {
			t.Fatalf("read listen_port: %v", err)
		}
		if port != 4001 {
			t.Errorf("listen_port = %d, want 4001", port)
		}
	})

	t.Run("read accent hex", func(t *testing.T) {
		var accent string
		if err := s.Get("config").Document().Get("theme").Get("accent").Value(&accent); err != nil {
			t.Fatalf("read accent: %v", err)
		}
		if accent != "#E3893F" {
			t.Errorf("accent = %q, want #E3893F", accent)
		}
	})

	t.Run("write through Set + Commit + Sync", func(t *testing.T) {
		if err := s.Get("config").Document().Get("theme").Get("accent").Set("#5B9BD1").Commit(); err != nil {
			t.Fatalf("set + commit: %v", err)
		}
		// Commit just stages the document in the in-memory cache; Sync
		// is what writes to disk. Without this call, re-opening shows
		// the on-disk value unchanged.
		if err := s.Sync(); err != nil {
			t.Fatalf("sync: %v", err)
		}
		// Re-open with a fresh Seer so we read off disk, not cache.
		s2, err := New(SystemFS(dir))
		if err != nil {
			t.Fatalf("seer.New (reopen): %v", err)
		}
		var accent string
		if err := s2.Get("config").Document().Get("theme").Get("accent").Value(&accent); err != nil {
			t.Fatalf("re-read accent: %v", err)
		}
		if accent != "#5B9BD1" {
			t.Errorf("after Set+Commit+Sync, accent = %q, want #5B9BD1", accent)
		}
	})
}
