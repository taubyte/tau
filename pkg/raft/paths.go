package raft

import (
	"fmt"
	"io"
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
//
// TODO(remove early 2027): transitional shim for the <root>/storage/.../raft-snapshots
// -> <root>/raft layout change. Once all deployments have started at least once
// on the new layout, delete this, legacySnapshotDir, copyDir/copyFile, and the
// call in cli/node/start.go.
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
		// Legacy and current are usually on the same filesystem, but if storage/
		// is a separate mount the rename fails with EXDEV. Fall back to a copy,
		// and only drop the legacy dir once the copy fully succeeds so a failure
		// mid-copy leaves the original snapshots intact.
		if err := copyDir(legacy, current); err != nil {
			return fmt.Errorf("migrating raft snapshots %s -> %s (copy fallback): %w", legacy, current, err)
		}
		if err := os.RemoveAll(legacy); err != nil {
			return fmt.Errorf("removing legacy snapshot dir %s after copy: %w", legacy, err)
		}
	}
	return nil
}

// copyDir recursively copies src into dst, preserving file permissions. Raft
// snapshot directories contain only regular files and subdirectories.
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		return copyFile(p, target, info.Mode().Perm())
	})
}

func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
