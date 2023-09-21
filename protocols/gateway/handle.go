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

	matches := make([]*client.Response, 0)
	for response := range resCh {
		err := response.Error()
		if err != nil {
			logger.Debugf("response from node `%s` failed with: %s", response.PID().Pretty(), err.Error())
		}
		if err == nil && g.Get(response).Cached() {
			matches = append([]*client.Response{response}, matches...)
		} else {
			matches = append(matches, response)
		}
	}
	if len(matches) < 1 {
		return errors.New("no substrate match found")
	}
	defer func() {
		for _, match := range matches {
			match.Close()
		}
	}()

	w.Header().Add(ProxyHeader, matches[0].PID().Pretty())

	if err := tunnel.Frontend(w, r, matches[0]); err != nil {
		return fmt.Errorf("tunneling Frontend failed with: %w", err)
	}

	return nil
}
