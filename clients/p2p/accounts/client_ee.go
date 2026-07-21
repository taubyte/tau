//go:build ee

package accounts

import (
	"context"

	peerCore "github.com/libp2p/go-libp2p/core/peer"
	accountsIface "github.com/taubyte/tau/core/services/accounts"
	eep2p "github.com/taubyte/tau/ee/clients/p2p/accounts"
	peer "github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/streams/command"
)

// eeSurface is defined in the ee package and only aliased here; every method it
// carries is injected at construction — none is spelled or called in this tree.
type eeSurface = eep2p.Surface

func New(ctx context.Context, node peer.Node) (accountsIface.Client, error) {
	c, err := newBase(ctx, node)
	if err != nil {
		return nil, err
	}
	c.eeSurface = eep2p.NewSurface(c.send)
	return c, nil
}

func (c *Client) Peers(peers ...peerCore.ID) accountsIface.Client {
	cp := *c
	cp.peers = peers
	cp.eeSurface = eep2p.NewSurface(cp.send) // rebind to the new peer set
	return &cp
}

// send issues a stream command to the account service peers.
func (c *Client) send(verb string, body command.Body) (map[string]any, error) {
	return c.client.Send(verb, body, c.peers...)
}
