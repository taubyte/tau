// Package consensus is the agreement the services of an instance order their writes
// with, as a service of the instance: raft groups among the nodes of one cloud,
// opened on the instance's peer with the operator's timing, their snapshots under the
// instance's directory. Agreement between nodes that do not trust one another,
// pkg/cspbft, is offered here next.
package consensus

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/raft"
)

type descriptor struct {
	Name string
}

// Descriptor is the role a node runs this as. It serves no RPC of its own: each
// group is the raft protocol under its namespace.
var Descriptor = descriptor{Name: "consensus"}

// Config is how this instance's groups time out; zero is raft's defaults.
type Config struct {
	Timeouts         raft.TimeoutConfig `yaml:"timeouts"`
	BootstrapTimeout time.Duration      `yaml:"bootstrap_timeout"`
}

var (
	// ErrNotStarted is a group asked of a consensus service that has not started.
	ErrNotStarted = errors.New("consensus: not started")
	// ErrNoRoot is a group asked of an instance with no directory to keep its
	// snapshots in.
	ErrNoRoot = errors.New("consensus: no directory to keep a group's snapshots in")
)

// Service is consensus as an instance runs it.
type Service interface {
	service.Service

	// Raft is the raft group namespace names, joined on this instance's peer. The
	// caller closes it; what is still open closes with the service.
	Raft(namespace string) (raft.Cluster, error)
}

// New is the consensus service, unstarted.
func New() Service { return &server{} }

type server struct {
	mu     sync.Mutex
	peer   peer.Node
	root   string
	cfg    Config
	opened []raft.Cluster
	closed bool
}

// ErrClosed is a group asked of a consensus service that has closed.
var ErrClosed = errors.New("consensus: closed")

func (s *server) Start(_ context.Context, h service.Host) error {
	var cfg Config
	if err := h.Config(&cfg); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.peer, s.root, s.cfg = h.Peer(), h.Root(), cfg
	return nil
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
	c, err := raft.New(s.peer, namespace, Options(s.root, s.cfg)...)
	if err != nil {
		return nil, fmt.Errorf("consensus: %s: %w", namespace, err)
	}
	s.opened = append(s.opened, c)
	return c, nil
}

// Options are what a group is opened with on an instance rooted at root.
func Options(root string, cfg Config) []raft.Option {
	options := []raft.Option{raft.WithSnapshotRoot(filepath.Join(root, "raft"))}
	if cfg.Timeouts != (raft.TimeoutConfig{}) {
		options = append(options, raft.WithTimeouts(cfg.Timeouts))
	}
	if cfg.BootstrapTimeout != 0 {
		options = append(options, raft.WithBootstrapTimeout(cfg.BootstrapTimeout))
	}
	return options
}

func (s *server) Close() error {
	s.mu.Lock()
	opened := s.opened
	s.opened, s.closed = nil, true
	s.mu.Unlock()
	var errs []error
	for _, c := range opened {
		if err := c.Close(); err != nil && !errors.Is(err, raft.ErrAlreadyClosed) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
