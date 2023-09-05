package gateway

import (
	"errors"
	"fmt"

	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p/core/peer"
)

type handleRequest struct {
	requestMethod string // specific type
	// matchDefs...
}

func (g *Gateway) handle(req handleRequest) error {
	project, resource, err := g.match(Matcher{})
	if err != nil {
		return err
	}

	// need better names
	peersHave, others, err := g.discover(project, resource)
	if err != nil {
		return err
	}

	var bestPeer peer.ID
	var bestScore int
	for peer, res := range peersHave {
		// get res data compare against last best
		res.Get("")
		var score int
		if score > bestScore {
			bestScore = score
			bestPeer = peer
		}
	}

	if len(bestPeer) < 1 {
		for peer, err := range others {
			if errors.Is(err, errors.New("still good")) {
				bestPeer = peer
				break
			}
		}
	}

	if len(bestPeer) < 1 {
		return errors.New("no available peers")
	}

	_cid, err := cid.Decode(bestPeer.String())
	if err != nil {
		return fmt.Errorf("decoding peer ID failed with: %w", err)
	}

	body := make(map[string]interface{}, 2)
	body["project"], body["resource"] = project, resource

	// maybe something else
	res, err := g.p2pClient.SendTo(_cid, "handle_meta", body)
	if err != nil {
		return fmt.Errorf("getting handle meta failed with: %w", err)
	}

	// get someMetaData, and return
	res.Get("")

	return nil

}

func (g *Gateway) handleHttp() error {
	return g.handle()
}
