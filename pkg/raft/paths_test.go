package raft

import (
	"os"
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

// seedSnapshot writes a fake snapshot entry into dir.
func seedSnapshot(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name, "meta.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateSnapshotDir(t *testing.T) {
	t.Run("moves legacy snapshots to the new layout", func(t *testing.T) {
		root := t.TempDir()
		legacy := legacySnapshotDir(root, "prod", "main")
		seedSnapshot(t, legacy, "2-1065-snap")

		if err := MigrateSnapshotDir(root, "prod", "main"); err != nil {
			t.Fatal(err)
		}

		current := SnapshotDir(root, "prod", "main")
		if _, err := os.Stat(filepath.Join(current, "2-1065-snap", "meta.json")); err != nil {
			t.Fatalf("snapshot not moved to new layout: %v", err)
		}
		if _, err := os.Stat(legacy); !os.IsNotExist(err) {
			t.Fatalf("legacy dir should be gone after move, err=%v", err)
		}
	})

	t.Run("does not clobber an already-populated new dir", func(t *testing.T) {
		root := t.TempDir()
		seedSnapshot(t, legacySnapshotDir(root, "prod", "main"), "old-snap")
		current := SnapshotDir(root, "prod", "main")
		seedSnapshot(t, current, "new-snap")

		if err := MigrateSnapshotDir(root, "prod", "main"); err != nil {
			t.Fatal(err)
		}

		// new dir's snapshot is kept; legacy is left untouched (not merged)
		if _, err := os.Stat(filepath.Join(current, "new-snap")); err != nil {
			t.Fatalf("existing new snapshot must be preserved: %v", err)
		}
		if _, err := os.Stat(filepath.Join(current, "old-snap")); !os.IsNotExist(err) {
			t.Fatalf("legacy snapshot must not overwrite newer state")
		}
	})

	t.Run("no legacy dir is a no-op", func(t *testing.T) {
		root := t.TempDir()
		if err := MigrateSnapshotDir(root, "prod", "main"); err != nil {
			t.Fatalf("expected no-op, got %v", err)
		}
	})
}

// TestCopyDir covers the cross-filesystem fallback used by MigrateSnapshotDir
// when os.Rename fails (EXDEV), which can't be forced portably in a unit test.
func TestCopyDir(t *testing.T) {
	src := t.TempDir()
	seedSnapshot(t, src, "2-1065-snap")
	if err := os.WriteFile(filepath.Join(src, "top.bin"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(t.TempDir(), "moved")
	if err := copyDir(src, dst); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(dst, "2-1065-snap", "meta.json"))
	if err != nil || string(got) != "{}" {
		t.Fatalf("nested file not copied: got %q err=%v", got, err)
	}
	got, err = os.ReadFile(filepath.Join(dst, "top.bin"))
	if err != nil || string(got) != "data" {
		t.Fatalf("top-level file not copied: got %q err=%v", got, err)
	}
	// permissions preserved
	info, err := os.Stat(filepath.Join(dst, "top.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("permission not preserved: got %v", info.Mode().Perm())
	}
}
