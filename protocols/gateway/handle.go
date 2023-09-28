package gateway

import (
	"errors"
	"fmt"
	"sort"

	goHttp "net/http"

	http "github.com/taubyte/http"
	"github.com/taubyte/p2p/streams/client"
	tunnel "github.com/taubyte/p2p/streams/tunnels/http"
	"github.com/taubyte/tau/protocols/substrate/components/metrics"
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

type wrappedResponse[T metrics.Function | metrics.Website] struct {
	metrics T
	*client.Response
}

func (wr wrappedResponse[T]) Decode(data interface{}) error {
	switch metricsData := data.(type) {
	case []byte:
		var m T
		return metrics.Iface(m).Decode(metricsData)
	default:
		return errors.New("metrics data not []byte")
	}
}

func (g *Gateway) handleHttp(w goHttp.ResponseWriter, r *goHttp.Request) error {
	resCh, err := g.substrateClient.ProxyHTTP(r.Host, r.URL.Path, r.Method)
	if err != nil {
		return fmt.Errorf("substrate client proxyHttp failed with: %w", err)
	}

	websiteMatches := make([]wrappedResponse[metrics.Website], 0)
	funcMatches := make([]wrappedResponse[metrics.Function], 0)
	discard := make([]*client.Response, 0)
	for response := range resCh {
		err := response.Error()
		if err != nil {
			logger.Debugf("response from node `%s` failed with: %s", response.PID().Pretty(), err.Error())
		}

		if _metrics, err := response.Get("website"); err == nil {
			wres := wrappedResponse[metrics.Website]{Response: response}
			err = wres.Decode(_metrics)
			if err == nil {
				websiteMatches = append([]wrappedResponse[metrics.Website]{wres}, websiteMatches...)
				continue
			}
		}

		if _metrics, err := response.Get("function"); err == nil {
			wres := wrappedResponse[metrics.Function]{Response: response}
			err = wres.Decode(_metrics)
			if err == nil {
				funcMatches = append([]wrappedResponse[metrics.Function]{wres}, funcMatches...)
				continue
			}
		}

		// all else
		discard = append(discard, response)
	}

	if len(websiteMatches) == 0 && len(funcMatches) == 0 {
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

	w.Header().Add(ProxyHeader, pick.PID().Pretty())

	if err := tunnel.Frontend(w, r, pick); err != nil {
		return fmt.Errorf("tunneling Frontend failed with: %w", err)
	}

	return nil
}
