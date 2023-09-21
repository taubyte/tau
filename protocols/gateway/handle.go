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

	var match *client.Response
	doneChan := make(chan struct{}, 1)
	var clearMode bool
	go func() {
		for response := range resCh {
			if clearMode {
				response.Close()
				continue
			}

			if err := response.Error(); err != nil {
				logger.Errorf("peer `%s` response for proxy failed with: %w", response.PID().Pretty(), err)
				continue
			}

			var skipClose bool
			if match == nil {
				match = response
				skipClose = true
			}

			if g.Get(response).Cached() {
				if !skipClose {
					match.Close()
				}

				match = response
				clearMode = true
				doneChan <- struct{}{}
			}

		}

		close(doneChan)
	}()
	<-doneChan

	if match == nil {
		return errors.New("no substrate match found")
	}
	defer match.Close()

	w.Header().Add(ProxyHeader, match.PID().Pretty())

	if err := tunnel.Frontend(w, r, match); err != nil {
		return fmt.Errorf("tunneling Frontend failed with: %w", err)
	}

	return nil
}
