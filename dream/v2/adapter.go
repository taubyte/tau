// Package v2 registers v2 services with dream, and is the one place the two
// shapes are reconciled.
//
// # The mismatch, and why it is an adapter rather than a retrofit
//
// dream needs a service satisfying `{ Node() peer.Node; Close() error }`. A v2
// service takes a `host.Host` and never a peer.Node — deliberately, and every v2
// service's config file says so: peer.Node bundles a host, a DHT, a datastore, a
// DAG and pubsub, and a test that wanted any one of them got a DHT bootstrap and
// an on-disk datastore with it.
//
// The bridge is one-way and it exists: `peer.Node.Peer()` hands out the
// host.Host (p2p/peer/type.go:46). So dream can build the bundle it wants, take
// the host out of it, and give the v2 service only that. Which means v2 keeps
// its testability AND runs under dream unchanged — the whole reason the
// constraint was worth holding.
//
// # What this package is careful about
//
// THE SERVICE DOES NOT OWN THE NODE. The node is built here and closed here; a
// service that closed the node it was handed would take down every other service
// sharing it. So the wrapper closes the service first and the node second, and
// the service's own Close never sees the node at all.
package v2

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/libp2p/go-libp2p/core/host"

	iface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/dream/common"
	"github.com/taubyte/tau/p2p/peer"
	tauConfig "github.com/taubyte/tau/pkg/config"
)

// Build makes one v2 service. It is handed the host taken out of the node dream
// built, plus the universe and the raw service config for anything else it
// needs — a pubsub bus, a port, a peer list.
//
// It returns an io.Closer because that is all the wrapper needs: a v2 service
// exposes no Node() and must not.
type Build func(ctx context.Context, h host.Host, node peer.Node, u *dream.Universe, cfg *iface.ServiceConfig) (io.Closer, error)

// Register wires a v2 service into dream under a protocol name.
//
// It is called from an init() in each service's dream package, the same way v1
// does it, so that importing the package is what makes the service available —
// and so a universe that never mentions the service pays nothing for it.
func Register(protocol string, build Build) error {
	if protocol == "" {
		return errors.New("dream/v2: a service with no protocol name")
	}
	if build == nil {
		return fmt.Errorf("dream/v2: %s has nothing to build", protocol)
	}
	return dream.Registry.Set(protocol, creator(protocol, build), nil)
}

func creator(protocol string, build Build) func(*dream.Universe, *iface.ServiceConfig) (iface.Service, error) {
	return func(u *dream.Universe, config *iface.ServiceConfig) (iface.Service, error) {
		cfg, err := common.NewConfig(u, config)
		if err != nil {
			return nil, fmt.Errorf("dream/v2: %s config: %w", protocol, err)
		}

		node, err := tauConfig.NewNode(u.Context(), cfg, config.Root)
		if err != nil {
			return nil, fmt.Errorf("dream/v2: %s node: %w", protocol, err)
		}

		svc, err := build(u.Context(), node.Peer(), node, u, config)
		if err != nil {
			// The node was built here, so it is closed here. A half-built
			// service that left a node running would leak a libp2p host and an
			// on-disk datastore per failed start.
			node.Close()
			return nil, fmt.Errorf("dream/v2: %s: %w", protocol, err)
		}

		return &service{node: node, svc: svc}, nil
	}
}

// service is a v2 service wearing the shape dream expects.
type service struct {
	node peer.Node
	svc  io.Closer
}

func (s *service) Node() peer.Node { return s.node }

// Close stops the service, then the node.
//
// THAT ORDER IS THE POINT. The service may still be publishing or answering when
// Close is called, and closing the node under it turns an orderly shutdown into
// a set of errors from a host that has gone. Both are closed even if the first
// fails, because a node left running is a port still bound.
func (s *service) Close() error { return closeBoth(s.svc, s.node.Close) }

// closeBoth is the ordering, split out so it is testable without a peer.Node —
// the property is about WHICH closes first and whether the second happens at
// all, and neither needs a libp2p host to state.
//
// The node arrives as a func rather than an io.Closer because peer.Node.Close
// returns nothing: it cannot fail, so there is no error to fold in and no
// interface it satisfies.
func closeBoth(svc io.Closer, closeNode func()) error {
	var svcErr error
	if svc != nil {
		svcErr = svc.Close()
	}
	if closeNode != nil {
		closeNode()
	}
	return svcErr
}
