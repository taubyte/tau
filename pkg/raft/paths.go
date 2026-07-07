package raft

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SnapshotDir returns <root>/raft/<shape>/<namespace> (empty namespace → "main"; "/" in namespace → "-").
// The directory is created by newSnapshotStore when the cluster starts.
func SnapshotDir(root, shape, namespace string) string {
	if namespace == "" {
		namespace = "main"
	}
	safe := strings.ReplaceAll(namespace, "/", "-")
	return filepath.Join(root, "raft", shape, safe)
}

// legacySnapshotDir returns the pre-scoping layout
// (<root>/storage/<shape>/raft-snapshots/<namespace>) that shipped before
// snapshots moved under <root>/raft. Retained only for MigrateSnapshotDir.
func legacySnapshotDir(root, shape, namespace string) string {
	if namespace == "" {
		namespace = "main"
	}
	safe := strings.ReplaceAll(namespace, "/", "-")
	return filepath.Join(root, "storage", shape, "raft-snapshots", safe)
}

// MigrateSnapshotDir relocates raft snapshots from the legacy layout to the
// current SnapshotDir layout. It is a one-time, idempotent operation safe to call
// on every startup: it moves the legacy directory only when it holds snapshots
// and the current directory does not yet (so it never clobbers newer state).
// Legacy and current live under the same root (same filesystem), so the move is
// an atomic rename. Callers with a persistent root (production) should call this
// before constructing the cluster; ephemeral roots (dream) have nothing to move.
func MigrateSnapshotDir(root, shape, namespace string) error {
	current := SnapshotDir(root, shape, namespace)
	legacy := legacySnapshotDir(root, shape, namespace)
	if current == legacy {
		return nil
	}

	legacyEntries, err := os.ReadDir(legacy)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // nothing to migrate
		}
		return fmt.Errorf("reading legacy snapshot dir %s: %w", legacy, err)
	}
	if len(legacyEntries) == 0 {
		return nil
	}

	// Never overwrite an already-populated current dir (already migrated / newer).
	if entries, err := os.ReadDir(current); err == nil && len(entries) > 0 {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(current), 0o755); err != nil {
		return fmt.Errorf("creating snapshot dir parent %s: %w", filepath.Dir(current), err)
	}
	// Remove an empty current dir so rename can claim the path.
	os.Remove(current)
	if err := os.Rename(legacy, current); err != nil {
		return fmt.Errorf("migrating raft snapshots %s -> %s: %w", legacy, current, err)
	}
	return nil
}
