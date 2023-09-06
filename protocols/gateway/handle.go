package gateway

import (
	"errors"
	"fmt"

	goHttp "net/http"

	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p/core/peer"
	http "github.com/taubyte/http"
)

func (g *Gateway) attach() {
	g.http.LowLevel(&http.LowLevelDefinition{
		PathPrefix: "/",
		Handler: func(w goHttp.ResponseWriter, r *goHttp.Request) {

		},
	})
}

func (g *Gateway) handleHttp(w goHttp.ResponseWriter, r *goHttp.Request) error {
	peersHave, peersDont, err := g.discover(r)
	if err != nil {
		return err
	}

	var bestPeer peer.ID
	var bestScore int
	for peer, res := range peersHave {
		// get res data compare against last best
		isCached, err := res.Get("cached")
		if err != nil {
			logger.Errorf("getting `cached` response from peer failed with: %s", err.Error())
			continue
		}

		cached,ok := isCached.(bool)
		if  
		
		var score int
		if score > bestScore {
			/*
				res for now will just say cached = true
			*/
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
