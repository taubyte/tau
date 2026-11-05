package cm

import (
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/taubyte/tau/pkg/raft"
)

// consensus.go is the agreement the services of an instance order their writes
// with: raft groups among the nodes of one cloud, opened on the instance's peer
// with the operator's timing, their snapshots under the instance's directory. Who
// may join a group, and where it lives, is cm's to decide as the cloud's manager;
// it is never on a group's write path, which applies in the process that opened it.

// ConsensusConfig is how an instance's groups time out; zero is raft's defaults.
type ConsensusConfig struct {
	Timeouts         raft.TimeoutConfig `yaml:"timeouts"`
	BootstrapTimeout time.Duration      `yaml:"bootstrap_timeout"`
}

// Consensus opens the raft group namespace names, joined on this instance's peer.
// The caller closes it; what is still open closes with cm.
type Consensus interface {
	Raft(namespace string) (raft.Cluster, error)
}

var (
	// ErrNotStarted is a group asked of a cm that has not started.
	ErrNotStarted = errors.New("cm: not started")
	// ErrNoRoot is a group asked of an instance with no directory to keep its
	// snapshots in.
	ErrNoRoot = errors.New("cm: no directory to keep a group's snapshots in")
	// ErrClosed is a group asked of a cm that has closed.
	ErrClosed = errors.New("cm: closed")
)

// RaftOptions are what a group is opened with on an instance rooted at root.
func RaftOptions(root string, cfg ConsensusConfig) []raft.Option {
	options := []raft.Option{raft.WithSnapshotRoot(filepath.Join(root, "raft"))}
	if cfg.Timeouts != (raft.TimeoutConfig{}) {
		options = append(options, raft.WithTimeouts(cfg.Timeouts))
	}
	if cfg.BootstrapTimeout != 0 {
		options = append(options, raft.WithBootstrapTimeout(cfg.BootstrapTimeout))
	}
	return options
}

func (s *server) Raft(namespace string) (raft.Cluster, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.closed:
		return nil, ErrClosed
	case s.peer == nil:
		return nil, ErrNotStarted
	case s.root == "":
		return nil, ErrNoRoot
	}
	c, err := raft.New(s.peer, namespace, RaftOptions(s.root, s.timing)...)
	if err != nil {
		return nil, fmt.Errorf("cm: %s: %w", namespace, err)
	}
	s.opened = append(s.opened, c)
	return c, nil
}
