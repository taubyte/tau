package raft

import (
	"path/filepath"
	"strings"
)

// SnapshotDir returns <root>/storage/<shape>/raft-snapshots/<namespace> (empty namespace → "main"; "/" in namespace → "-").
func SnapshotDir(root, shape, namespace string) string {
	if namespace == "" {
		namespace = "main"
	}
	safe := strings.ReplaceAll(namespace, "/", "-")
	return filepath.Join(root, "storage", shape, "raft-snapshots", safe)
}
