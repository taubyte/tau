package raft

import (
	"crypto/aes"
	"crypto/cipher"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

type Option func(*cluster) error

func WithTimeouts(cfg TimeoutConfig) Option {
	return func(c *cluster) error {
		c.timeoutConfig = cfg
		return nil
	}
}

// WithForceBootstrap bootstraps a group of one at once, skipping discovery.
// Only for a node known to be the first of a new group.
func WithForceBootstrap() Option {
	return func(c *cluster) error {
		c.forceBootstrap = true
		return nil
	}
}

// WithBootstrapTimeout is how long to look for peers before bootstrapping
// alone. Default: 30s.
func WithBootstrapTimeout(d time.Duration) Option {
	return func(c *cluster) error {
		c.bootstrapTimeout = d
		return nil
	}
}

// WithSnapshotDir sets the snapshot directory (default: a folder of the
// node's own, raft/<namespace>).
func WithSnapshotDir(dir string) Option {
	return func(c *cluster) error {
		if dir == "" {
			return fmt.Errorf("snapshot dir cannot be empty")
		}
		c.snapshotDir = dir
		return nil
	}
}

// WithSnapshotRoot puts snapshots under root/<namespace>, a directory the
// caller owns. Without it they go in a folder of the node's own, so two nodes
// never share one. The namespace still names the subdirectory because one node
// runs many groups.
func WithSnapshotRoot(root string) Option {
	return func(c *cluster) error {
		if root == "" {
			return fmt.Errorf("snapshot root cannot be empty")
		}
		c.snapshotDir = filepath.Join(root, strings.ReplaceAll(c.namespace, "/", "_"))
		return nil
	}
}

// WithEncryptionKey seals the raft transport and every call with AES-256-GCM.
// The key is 32 bytes, the same on every member.
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
