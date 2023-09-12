package gateway

import (
	"errors"
	"fmt"

	goHttp "net/http"

	"github.com/libp2p/go-libp2p/core/peer"
	http "github.com/taubyte/http"
)

func (g *Gateway) attach() {
	g.http.LowLevel(&http.LowLevelDefinition{
		PathPrefix: "/",
		Handler: func(w goHttp.ResponseWriter, r *goHttp.Request) {
			if err := g.handle(w, r); err != nil {
				w.Write([]byte(err.Error()))
				w.WriteHeader(500)
			}
		},
	})
}

func (g *Gateway) handle(w goHttp.ResponseWriter, r *goHttp.Request) error {
	peerResponses, _, err := g.substrateClient.Has(r.Host, r.URL.Path, r.Method, g.threshold())
	if err != nil {
		return fmt.Errorf("substrate client Has failed with: %w", err)
	}

	var bestPeer peer.ID
	// even if all peers have a 0 score, a peer will be selected
	bestScore := -1
	for peer, res := range peerResponses {
		var score int
		responseGetter := g.Get(res)
		if responseGetter.Cached() {
			score += 50
		}

		// currently just check if serviceable is cached, later have geo info,memory etc.
		if score > bestScore {
			bestScore = score
			bestPeer = peer
		}
	}
	if len(bestPeer) < 1 {
		return errors.New("no available peers")
	}

	res, err := g.substrateClient.Handle(bestPeer)
	if err != nil {
		return fmt.Errorf("substrate client Handle failed with: %w", err)
	}

	// This below is all just for proof of concept, not used for real
	peerIface, err := res.Get("peer")
	if err != nil {
		return fmt.Errorf("peer get failed with: %w", err)
	}

	peer, ok := peerIface.(string)
	if !ok {
		return fmt.Errorf("peer not string so not ok")
	}

	if bestPeer.String() != peer {
		return fmt.Errorf("expected send and retrieve peer to be same")
	}

	w.Write([]byte(peer))
	w.WriteHeader(200)

	return nil
}
