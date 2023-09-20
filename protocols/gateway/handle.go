package gateway

import (
	"errors"
	"fmt"

	goHttp "net/http"

	"github.com/libp2p/go-libp2p/core/peer"
	http "github.com/taubyte/http"
	"github.com/taubyte/p2p/streams/client"
	tunnel "github.com/taubyte/p2p/streams/tunnels/http"
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
	peerResponses, _, err := g.substrateClient.ProxyStreams(r.Host, r.URL.Path, r.Method)
	if err != nil {
		return fmt.Errorf("substrate client Has failed with: %w", err)
	}

	match, err := g.match(peerResponses)
	if err != nil {
		return fmt.Errorf("matching substrate peers to handle request failed with: %w", err)
	}

	w.Header().Add("X-Substrate-Peer", match.PID().Pretty())

	if err := tunnel.Frontend(w, r, match); err != nil {
		return err
	}

	return nil
}

func (g *Gateway) match(responses map[peer.ID]*client.Response) (match *client.Response, err error) {
	// even if all peers have a 0 score, a peer will be selected
	bestScore := -1
	defer func() {
		if match == nil {
			err = errors.New("no available peers")
		}
	}()

	for _, res := range responses {
		var score int
		responseGetter := g.Get(res)
		if responseGetter.Cached() {
			score += 50
		}

		// currently just check if serviceable is cached, later have geo info,memory etc.
		if score > bestScore {
			bestScore = score
			match = res
		}
	}

	return
}
