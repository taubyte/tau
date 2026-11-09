package raft

import (
	"crypto/aes"
	"crypto/cipher"
	"fmt"
	"time"

	"github.com/taubyte/tau/p2p/rpc"
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

// WithClientRetry is how the member's calls to other members wait for one that is
// not answering on the group's protocol yet, as while it is rebuilt: the node's
// clients.retry, the same for every client on it.
func WithClientRetry(retry rpc.Retry) Option {
	return func(c *cluster) error {
		if err := retry.Check(); err != nil {
			return err
		}
		c.clientRetry = retry
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
