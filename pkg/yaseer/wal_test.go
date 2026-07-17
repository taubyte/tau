package seer

import (
	"strings"
	"testing"

	"github.com/spf13/afero"
)

// TestWAL_SuccessfulSyncClearsLog: after a normal Commit + Sync with
// WAL enabled, the WAL file is gone — Sync truncates it because the
// data file is already durable.
func TestWAL_SuccessfulSyncClearsLog(t *testing.T) {
	memfs := afero.NewMemMapFs()
	s, err := New(VirtualFS(memfs, "/"), WithWAL(".wal"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := s.Get("config").Document().Get("theme").Set("dark").Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := s.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if _, err := memfs.Stat("/.wal"); err == nil {
		t.Error("WAL still present after successful Sync — should have been cleared")
	}
}

// TestWAL_CommitWithoutSyncIsDurable: simulate the canonical crash
// case. Commit() is called (WAL frame appended + fsynced), then the
// process disappears before Sync. A fresh New() must replay the
// frame and produce the same disk state as if Sync had run.
func TestWAL_CommitWithoutSyncIsDurable(t *testing.T) {
	memfs := afero.NewMemMapFs()
	s, err := New(VirtualFS(memfs, "/"), WithWAL(".wal"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Commit a write — WAL frame appended, in-memory doc updated,
	// but Sync NOT called: the value never reaches the data file.
	// (Document() does touch the file as a side effect, so the file
	// can exist after Commit — just without the set value.)
	if err := s.Get("config").Document().Get("theme").Set("blue").Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if body, _ := afero.ReadFile(memfs, "/config.yaml"); strings.Contains(string(body), "theme: blue") {
		t.Fatal("theme: blue shouldn't be on disk yet — Sync hasn't run")
	}

	// Simulate restart by dropping the Seer instance and building a
	// fresh one against the same FS. The constructor's replayWAL
	// should re-apply the frame and Sync.
	_, err = New(VirtualFS(memfs, "/"), WithWAL(".wal"))
	if err != nil {
		t.Fatalf("New (replay): %v", err)
	}
	got, err := afero.ReadFile(memfs, "/config.yaml")
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(got), "theme: blue") {
		t.Errorf("after replay, config didn't contain theme: blue\n  got: %q", got)
	}
	if _, err := memfs.Stat("/.wal"); err == nil {
		t.Error("WAL still present after replay-and-sync")
	}
}

// TestWAL_HumanEditPreserved: between crash and restart a human
// modifies the YAML. Replay applies our pending op on top of the
// human edit, not over it — both changes survive when they target
// different keys.
func TestWAL_HumanEditPreserved(t *testing.T) {
	memfs := afero.NewMemMapFs()

	// Baseline state: write a file the human-edit step can modify.
	if err := afero.WriteFile(memfs, "/config.yaml",
		[]byte("theme: dark\n"), 0o600); err != nil {
		t.Fatalf("baseline write: %v", err)
	}

	// Start Seer A, commit a change to a DIFFERENT key, but don't
	// Sync (simulating crash before flush).
	sA, err := New(VirtualFS(memfs, "/"), WithWAL(".wal"))
	if err != nil {
		t.Fatalf("New A: %v", err)
	}
	if err := sA.Get("config").Document().Get("accent").Set("#5B9BD1").Commit(); err != nil {
		t.Fatalf("commit accent: %v", err)
	}

	// Human edits the file before we restart, changing theme to light.
	humanEdit := []byte("theme: light\n# user changed this\n")
	if err := afero.WriteFile(memfs, "/config.yaml", humanEdit, 0o600); err != nil {
		t.Fatalf("human edit: %v", err)
	}

	// Restart. Replay should load the file with `theme: light`,
	// apply the accent Set on top, and write back BOTH values.
	_, err = New(VirtualFS(memfs, "/"), WithWAL(".wal"))
	if err != nil {
		t.Fatalf("New B (replay): %v", err)
	}
	got, err := afero.ReadFile(memfs, "/config.yaml")
	if err != nil {
		t.Fatalf("read after replay: %v", err)
	}
	if !strings.Contains(string(got), "theme: light") {
		t.Errorf("human edit lost: %q", got)
	}
	if !strings.Contains(string(got), "accent: '#5B9BD1'") &&
		!strings.Contains(string(got), `accent: "#5B9BD1"`) {
		t.Errorf("pending op lost: %q", got)
	}
}

// TestWAL_RoundTripFrame: encode a representative ops slice, parse
// it back, verify wire codes survive.
func TestWAL_RoundTripFrame(t *testing.T) {
	memfs := afero.NewMemMapFs()
	s, err := New(VirtualFS(memfs, "/"), WithWAL(".wal"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Build an ops list by exercising the public Query API so the
	// handlers match the ones live commits use.
	q := s.Get("foo").Get("bar").Document().Get("baz").Set("value")
	body, err := encodeOpsFrame(q.ops)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	rebuilt, err := decodeOpsFrame(body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rebuilt) != len(q.ops) {
		t.Fatalf("opCount mismatch: got %d, want %d", len(rebuilt), len(q.ops))
	}
	for i := range q.ops {
		if rebuilt[i].opType != q.ops[i].opType {
			t.Errorf("op[%d] opType = %d, want %d", i, rebuilt[i].opType, q.ops[i].opType)
		}
		if rebuilt[i].name != q.ops[i].name {
			t.Errorf("op[%d] name = %q, want %q", i, rebuilt[i].name, q.ops[i].name)
		}
	}
}

// TestWAL_TornAppendIsDiscarded: a frame whose tail bytes are
// missing (mid-append crash before fsync completed) must be ignored
// — but everything BEFORE it should still replay.
func TestWAL_TornAppendIsDiscarded(t *testing.T) {
	memfs := afero.NewMemMapFs()
	s, err := New(VirtualFS(memfs, "/"), WithWAL(".wal"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Two clean Commits — both should land in the WAL.
	if err := s.Get("config").Document().Get("theme").Set("dark").Commit(); err != nil {
		t.Fatalf("commit 1: %v", err)
	}
	if err := s.Get("config").Document().Get("accent").Set("#E3893F").Commit(); err != nil {
		t.Fatalf("commit 2: %v", err)
	}

	// Truncate the WAL by 3 bytes to simulate a mid-frame crash on
	// the SECOND commit. The first should still replay; the second
	// is lost.
	walBytes, err := afero.ReadFile(memfs, "/.wal")
	if err != nil {
		t.Fatalf("read wal: %v", err)
	}
	if len(walBytes) < 4 {
		t.Fatalf("wal unexpectedly short (%d bytes)", len(walBytes))
	}
	if err := afero.WriteFile(memfs, "/.wal", walBytes[:len(walBytes)-3], 0o600); err != nil {
		t.Fatalf("truncate wal: %v", err)
	}

	// Restart. First frame should replay (theme dark stays); second
	// is gone (accent never set).
	_, err = New(VirtualFS(memfs, "/"), WithWAL(".wal"))
	if err != nil {
		t.Fatalf("New (replay torn): %v", err)
	}
	got, err := afero.ReadFile(memfs, "/config.yaml")
	if err != nil {
		t.Fatalf("read after replay: %v", err)
	}
	if !strings.Contains(string(got), "theme: dark") {
		t.Errorf("first commit lost: %q", got)
	}
	if strings.Contains(string(got), "accent") {
		t.Errorf("torn second commit should NOT have applied: %q", got)
	}
}
