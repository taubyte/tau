package gateway

import (
	"context"
	"errors"
	"fmt"

	goHttp "net/http"

	http "github.com/taubyte/http"
	"github.com/taubyte/p2p/streams/client"
	tunnel "github.com/taubyte/p2p/streams/tunnels/http"
)

func (g *Gateway) attach() {
	g.http.LowLevel(&http.LowLevelDefinition{
		PathPrefix: "/",
		Handler: func(w goHttp.ResponseWriter, r *goHttp.Request) {
			if err := g.handleHttp(w, r); err != nil {
				w.Write([]byte(err.Error()))
				w.WriteHeader(500)
			}
		},
	})
}

func (g *Gateway) handleHttp(w goHttp.ResponseWriter, r *goHttp.Request) error {
	resCh, err := g.substrateClient.ProxyHTTP(r.Host, r.URL.Path, r.Method)
	if err != nil {
		return fmt.Errorf("substrate client proxyHttp failed with: %w", err)
	}

	responses := make([]*client.Response, 0)
	var done bool
	ctx, ctxC := context.WithTimeout(g.ctx, ChannelTimeout)
	for !done {
		select {
		case <-ctx.Done():
			done = true
		case response, ok := <-resCh:
			if !ok {
				done = true
				break
			}

			if err := response.Error(); err == nil {
				responses = append(responses, response)
			}
		}
	}
	ctxC()

	match, err := g.match(responses)
	if err != nil {
		return fmt.Errorf("matching substrate peers to handle request failed with: %w", err)
	}

	w.Header().Add(ProxyHeader, match.PID().Pretty())

	if err := tunnel.Frontend(w, r, match); err != nil {
		return err
	}

	return nil
}

func (g *Gateway) match(responses []*client.Response) (match *client.Response, err error) {
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
