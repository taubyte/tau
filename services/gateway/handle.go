package gateway

import (
	"errors"
	"fmt"
	"sort"

	goHttp "net/http"

	"github.com/taubyte/tau/p2p/streams/client"
	tunnel "github.com/taubyte/tau/p2p/streams/tunnels/http"
	http "github.com/taubyte/tau/pkg/http"
	functionSpec "github.com/taubyte/tau/pkg/specs/function"
	websiteSpec "github.com/taubyte/tau/pkg/specs/website"
	"github.com/taubyte/tau/services/substrate/components/metrics"
)

func (g *Gateway) attach() {
	g.http.LowLevel(&http.LowLevelDefinition{
		PathPrefix: "/",
		Handler: func(w goHttp.ResponseWriter, r *goHttp.Request) {
			if err := g.handleHttp(w, r); err != nil {
				w.WriteHeader(500)
				w.Write([]byte(err.Error()))
			}
		},
	})
}

func (wr wrappedResponse) Decode(data interface{}) (err error) {
	switch metricsData := data.(type) {
	case []byte:
		return wr.metrics.Decode(metricsData)
	default:
		return errors.New("metrics data not []byte")
	}
}

func (g *Gateway) handleHttp(w goHttp.ResponseWriter, r *goHttp.Request) error {
	resCh, err := g.substrateClient.ProxyHTTP(r.Host, r.URL.Path, r.Method)
	if err != nil {
		return fmt.Errorf("substrate client proxyHttp failed with: %w", err)
	}

	websiteMatches := make([]wrappedResponse, 0)
	funcMatches := make([]wrappedResponse, 0)
	discard := make([]*client.Response, 0)
	for response := range resCh {
		if err := response.Error(); err != nil {
			logger.Debugf("response from node `%s` failed with: %s", response.PID().String(), err.Error())
		}

		if _metrics, err := response.Get(websiteSpec.PathVariable.String()); err == nil {
			wres := wrappedResponse{Response: response, metrics: new(metrics.Website)}
			if err = wres.Decode(_metrics); err == nil {
				websiteMatches = append(websiteMatches, wres)
				continue
			}
		}

		if _metrics, err := response.Get(functionSpec.PathVariable.String()); err == nil {
			wres := wrappedResponse{Response: response, metrics: new(metrics.Function)}
			if err = wres.Decode(_metrics); err == nil {
				funcMatches = append(funcMatches, wres)
				continue
			}
		}

		// all else
		discard = append(discard, response)
	}
	defer func() {
		for _, res := range discard {
			res.Close()
		}
		for _, res := range websiteMatches {
			res.Close()
		}
		for _, res := range funcMatches {
			res.Close()
		}
	}()
	if len(websiteMatches)+len(funcMatches) < 1 {
		return errors.New("no substrate match found")
	}

	var pick *client.Response
	if len(websiteMatches) > len(funcMatches) {
		sort.Slice(websiteMatches, func(i, j int) bool { return websiteMatches[j].metrics.Less(websiteMatches[i].metrics) })
		pick = websiteMatches[0].Response
	} else {
		sort.Slice(funcMatches, func(i, j int) bool { return funcMatches[j].metrics.Less(funcMatches[i].metrics) })
		pick = funcMatches[0].Response
	}

	w.Header().Add(ProxyHeader, pick.PID().String())

	if err := tunnel.Frontend(w, r, pick); err != nil {
		return fmt.Errorf("tunneling Frontend failed with: %w", err)
	}

	return nil
}
