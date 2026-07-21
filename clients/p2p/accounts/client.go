//go:build !ee

package accounts

import (
	"context"

	peerCore "github.com/libp2p/go-libp2p/core/peer"
	accountsIface "github.com/taubyte/tau/core/services/accounts"
	peer "github.com/taubyte/tau/p2p/peer"
)

// eeSurface adds no methods in the community build — linkage is the whole
// access model. Validate lives in validate.go.
type eeSurface interface{}

func New(ctx context.Context, node peer.Node) (accountsIface.Client, error) {
	return newBase(ctx, node)
}

func (c *Client) Peers(peers ...peerCore.ID) accountsIface.Client {
	cp := *c
	cp.peers = peers
	return &cp
}
