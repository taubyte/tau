package raft

import (
	"crypto/aes"
	"crypto/cipher"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// Option configures optional cluster behavior
type Option func(*cluster) error

// WithTimeouts sets custom timeout configuration
func WithTimeouts(cfg TimeoutConfig) Option {
	return func(c *cluster) error {
		c.timeoutConfig = cfg
		return nil
	}
}

// WithForceBootstrap forces immediate bootstrap as a single-node cluster,
// skipping peer discovery entirely.
// Default: false (discover peers first, auto-bootstrap only if none found)
// Use this only when you KNOW this should be the first node in a new cluster.
func WithForceBootstrap() Option {
	return func(c *cluster) error {
		c.forceBootstrap = true
		return nil
	}
}

// WithBootstrapTimeout sets how long to wait for peers before auto-bootstrapping.
// If no peers are discovered within this timeout, the node will bootstrap itself.
// Default: 10s
func WithBootstrapTimeout(d time.Duration) Option {
	return func(c *cluster) error {
		c.bootstrapTimeout = d
		return nil
	}
}

// WithSnapshotDir sets the snapshot directory (default /tmp/tau-raft-snapshots/<namespace>).
func WithSnapshotDir(dir string) Option {
	return func(c *cluster) error {
		if dir == "" {
			return fmt.Errorf("snapshot dir cannot be empty")
		}
		c.snapshotDir = dir
		return nil
	}
}

// WithSnapshotRoot puts this cluster's snapshots under a directory the CALLER
// owns, in a subdirectory named after the namespace.
//
// IT IS THE OPTION A COMPOSITION THAT RUNS TWO NODES NEEDS. The default is
// /tmp/tau-raft-snapshots/<namespace>, which is a machine-wide path: two nodes
// in one process, or two processes on one machine, share one snapshot store per
// namespace and write each other's state. Handing each node its own root is
// what makes that impossible rather than unlikely.
//
// THE NAMESPACE STILL NAMES THE SUBDIRECTORY, because one node opens many
// groups — one per resource in the case of a database — and a root shared
// between them would be the same collision one level down.
func WithSnapshotRoot(root string) Option {
	return func(c *cluster) error {
		if root == "" {
			return fmt.Errorf("snapshot root cannot be empty")
		}
		c.snapshotDir = filepath.Join(root, strings.ReplaceAll(c.namespace, "/", "_"))
		return nil
	}
}

// WithEncryptionKey enables AES-256-GCM encryption for all transport and stream service messages.
// The key must be at least 32 bytes. All cluster members must use the same key.
// This protects:
// - Raft RPC traffic (transport layer)
// - Stream service commands (set, get, delete, keys, joinVoter, exchangePeers)
// - Cluster join operations
func WithEncryptionKey(key []byte) Option {
	return func(c *cluster) error {
		if key != nil {
			if len(key) < 32 {
				return fmt.Errorf("encryption key must be at least 32 bytes, got %d", len(key))
			}
			block, err := aes.NewCipher(key)
			if err != nil {
				return fmt.Errorf("creating AES cipher: %w", err)
			}

			c.encryptionCipher, err = cipher.NewGCMWithNonceSize(block, nonceSize)
			if err != nil {
				return fmt.Errorf("creating GCM cipher: %w", err)
			}
		}
		return nil
	}
}
