package bridge

import (
	"context"
	"errors"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/p2p/keypair"
	p2p "github.com/taubyte/tau/p2p/peer"
)

// Config is what joining a cloud takes.
type Config struct {
	// SwarmKey is the cloud's pre-shared key, already parsed (utils.FormatSwarmKey).
	// It is required: libp2p given no key joins an OPEN network, which is
	// every swarm and none of them.
	SwarmKey []byte
	// Bootstrap are the nodes dialled first.
	Bootstrap []peer.AddrInfo
	// Cloud scopes the discovery topic, as node.Config.Cloud does.
	Cloud string
	// Listen are the swarm listen addresses. Empty is an ephemeral loopback
	// port: a light client accepts no inbound work.
	Listen []string
}

// Joined is a bridge that owns its peer.
type Joined struct {
	*Bridge
	node p2p.Node
}

// Join starts a light client peer in the cloud and a bridge on it.
func Join(ctx context.Context, cfg Config) (*Joined, error) {
	if len(cfg.SwarmKey) == 0 {
		return nil, errors.New("bridge: a swarm key is required; without one libp2p joins an open network")
	}
	if len(cfg.Bootstrap) == 0 {
		return nil, errors.New("bridge: at least one bootstrap peer is required")
	}
	listen := cfg.Listen
	if len(listen) == 0 {
		listen = []string{"/ip4/127.0.0.1/tcp/0"}
	}
	n, err := p2p.NewClientNode(ctx, nil, keypair.NewRaw(), cfg.SwarmKey, listen, nil, true, cfg.Bootstrap)
	if err != nil {
		return nil, err
	}
	b, err := New(ctx, n.Peer(), n.Messaging(), n.Discovery(), cfg.Cloud)
	if err != nil {
		n.Close()
		return nil, err
	}
	return &Joined{Bridge: b, node: n}, nil
}

// Close stops the bridge, then its peer.
func (j *Joined) Close() {
	j.Bridge.Close()
	j.node.Close()
}
