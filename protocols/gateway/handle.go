package gateway

import (
	"errors"
	"fmt"
	"io"

	goHttp "net/http"

	"github.com/libp2p/go-libp2p/core/peer"
	http "github.com/taubyte/http"
	"github.com/taubyte/p2p/streams/client"
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

func (g *Gateway) match(responses map[peer.ID]*client.Response) (match peer.ID, err error) {
	// even if all peers have a 0 score, a peer will be selected
	bestScore := -1
	defer func() {
		if len(match) < 1 {
			err = errors.New("no available peers")
		}
	}()

	for peer, res := range responses {
		var score int
		responseGetter := g.Get(res)
		if responseGetter.Cached() {
			score += 50
		}

		// currently just check if serviceable is cached, later have geo info,memory etc.
		if score > bestScore {
			bestScore = score
			match = peer
		}
	}

	return
}

func (g *Gateway) handle(w goHttp.ResponseWriter, r *goHttp.Request) error {
	peerResponses, _, err := g.substrateClient.Has(r.Host, r.URL.Path, r.Method, g.threshold())
	if err != nil {
		return fmt.Errorf("substrate client Has failed with: %w", err)
	}

	match, err := g.match(peerResponses)
	if err != nil {
		return fmt.Errorf("matching substrate peers to handle request failed with: %w", err)
	}

	res, err := g.substrateClient.Tunnel(match)
	if err != nil {
		return fmt.Errorf("substrate client Handle failed with: %w", err)
	}
	defer res.Close()

	data, err := io.ReadAll(res)
	if err != nil {
		return err
	}

	// // This below is all just for proof of concept, not used for real
	// peerIface, err := res.Get("peer")
	// if err != nil {
	// 	return fmt.Errorf("peer get failed with: %w", err)
	// }

	// peer, ok := peerIface.(string)
	// if !ok {
	// 	return fmt.Errorf("peer not string so not ok")
	// }

	// if match.String() != peer {
	// 	return fmt.Errorf("expected send and retrieve peer to be same")
	// }

	w.Write(data)
	w.WriteHeader(200)

	return nil
}
