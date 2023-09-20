package gateway

import (
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

	highScore := -1
	var match *client.Response
	for highScore < MaxScore {
		response, ok := <-resCh
		if !ok {
			break
		}
		if g.Get(response).Cached() {
			highScore = MaxScore
			match = response
		}
	}
	if match == nil {
		return errors.New("no substrate match found")
	}

	w.Header().Add(ProxyHeader, match.PID().Pretty())

	if err := tunnel.Frontend(w, r, match); err != nil {
		return err
	}

	return nil
}
