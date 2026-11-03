package bridge

import (
	"context"
	"errors"
	"fmt"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"

	p2p "github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/sectls"
)

type Config struct {
	// Key is this client's libp2p identity, marshalled. CA and Certificate
	// are PEM: the cloud's root, and a certificate from it for Key. They are
	// required: a cloud admits no peer without one.
	Key         []byte
	CA          []byte
	Certificate []byte
	Bootstrap   []peer.AddrInfo
	// Cloud scopes the discovery topic, as node.Config.Cloud does.
	Cloud string
	// Listen are the swarm listen addresses. Empty is an ephemeral loopback
	// port: a light client accepts no inbound work.
	Listen []string
}

type Joined struct {
	*Bridge
	node p2p.Node
}

// Join starts a light client peer in the cloud and a bridge on it.
func Join(ctx context.Context, cfg Config) (*Joined, error) {
	if len(cfg.Key) == 0 || len(cfg.CA) == 0 || len(cfg.Certificate) == 0 {
		return nil, errors.New("bridge: a key, the cloud's CA and a certificate from it are required")
	}
	if len(cfg.Bootstrap) == 0 {
		return nil, errors.New("bridge: at least one bootstrap peer is required")
	}
	listen := cfg.Listen
	if len(listen) == 0 {
		listen = []string{"/ip4/127.0.0.1/tcp/0"}
	}
	key, err := crypto.UnmarshalPrivateKey(cfg.Key)
	if err != nil {
		return nil, fmt.Errorf("bridge: key: %w", err)
	}
	admission, err := sectls.NewCredentials(cfg.CA, cfg.Certificate, cfg.Cloud, key)
	if err != nil {
		return nil, fmt.Errorf("bridge: %w", err)
	}
	n, err := p2p.NewClientNode(ctx, nil, cfg.Key, admission, listen, nil, true, cfg.Bootstrap)
	if err != nil {
		return nil, err
	}
	b, err := New(ctx, n.Host(), n.Messaging(), n.Discovery(), cfg.Cloud)
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
