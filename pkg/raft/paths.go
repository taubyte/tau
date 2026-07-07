package raft

import (
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
