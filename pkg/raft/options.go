package raft

import (
	"crypto/aes"
	"crypto/cipher"
	"fmt"
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

// WithQuorumLossRecovery has the members that remain of a group that lost its
// quorum form a new group carrying what they hold, once they have had no leader
// for after. Without it a group waits for its members to come back.
func WithQuorumLossRecovery(after time.Duration) Option {
	return func(c *cluster) error {
		if after <= 0 {
			return fmt.Errorf("quorum loss recovery after %v: must be above zero", after)
		}
		c.quorumLossAfter = after
		return nil
	}
}

// WithEncryptionKey seals the group's raft messages and every call with AES-256-GCM.
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
