package gateway

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	goHttp "net/http"

	"github.com/taubyte/tau/p2p/streams/client"
	tunnel "github.com/taubyte/tau/p2p/streams/tunnels/http"
	http "github.com/taubyte/tau/pkg/http"
	"github.com/taubyte/tau/pkg/platformerr"
	functionSpec "github.com/taubyte/tau/pkg/specs/function"
	websiteSpec "github.com/taubyte/tau/pkg/specs/website"
	"github.com/taubyte/tau/services/substrate/components/metrics"
)

func (g *Gateway) attach() {
	g.http.LowLevel(&http.LowLevelDefinition{
		PathPrefix: "/",
		Handler: func(w goHttp.ResponseWriter, r *goHttp.Request) {
			platformerr.Serve(w, r, logger, g.handleHttp)
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

// lookupTimeouts is how long each attempt waits for substrates to answer. The
// first is short because an attempt waits out its timeout looking for more
// peers, but it must let the other nodes answer: a match from the local node
// alone ends the lookup and leaves nothing to balance across. A busy node or a
// cold lookup after a deploy needs longer, and that must not read as "no match".
var lookupTimeouts = []time.Duration{30 * time.Millisecond, 200 * time.Millisecond, 2 * time.Second}

type lookupResult struct {
	websites, functions []wrappedResponse
	discard             []*client.Response
	notFound            bool // a substrate looked and has no match
	failed              bool // a substrate did not answer in time, or its lookup failed
}

func (l *lookupResult) close() {
	for _, res := range l.discard {
		res.Close()
	}
	for _, res := range l.websites {
		res.Close()
	}
	for _, res := range l.functions {
		res.Close()
	}
}

func (l *lookupResult) matched() bool { return len(l.websites)+len(l.functions) > 0 }

// definite says a further attempt cannot change the answer: the substrates
// that answered looked and found nothing, and none failed to look.
func (l *lookupResult) definite() bool { return l.notFound && !l.failed }

// notFound says a substrate looked and has no match. Any other error, a timeout
// or a lookup that failed under load, says nothing about whether the page exists.
func notFound(err error) bool {
	if code := platformerr.CodeOf(err); code != "" {
		return code == platformerr.NotFound
	}
	return legacyNotFound(err)
}

// shortcut: old substrates send no code; remove once every node runs this release.
func legacyNotFound(err error) bool {
	return strings.Contains(err.Error(), "no HTTP match found for method")
}

func (g *Gateway) lookupOnce(r *goHttp.Request, timeout time.Duration, threshold int) (*lookupResult, error) {
	var ops []client.Option[client.Request]
	if timeout > 0 {
		ops = append(ops, client.Timeout(timeout))
	}
	if threshold > 0 {
		ops = append(ops, client.Threshold(threshold))
	}

	resCh, err := g.substrateClient.ProxyHTTP(r.Host, r.URL.Path, r.Method, ops...)
	if err != nil {
		return nil, fmt.Errorf("substrate client proxyHttp failed with: %w", err)
	}

	result := new(lookupResult)
	for response := range resCh {
		if err := response.Error(); err != nil {
			logger.Debugf("response from node `%s` failed with: %s", response.PID().String(), err.Error())
			if notFound(err) {
				result.notFound = true
			} else {
				result.failed = true
			}
			result.discard = append(result.discard, response)
			continue
		}

		if _metrics, err := response.Get(websiteSpec.PathVariable.String()); err == nil {
			wres := wrappedResponse{Response: response, metrics: new(metrics.Website)}
			if err = wres.Decode(_metrics); err == nil {
				result.websites = append(result.websites, wres)
				continue
			}
		}

		if _metrics, err := response.Get(functionSpec.PathVariable.String()); err == nil {
			wres := wrappedResponse{Response: response, metrics: new(metrics.Function)}
			if err = wres.Decode(_metrics); err == nil {
				result.functions = append(result.functions, wres)
				continue
			}
		}

		result.discard = append(result.discard, response)
	}
	return result, nil
}

// lookup asks again with more time until there is a match or a certain answer.
func (g *Gateway) lookup(r *goHttp.Request) (*lookupResult, error) {
	threshold, timeouts := g.lookupPlan()
	var result *lookupResult
	for i, timeout := range timeouts {
		if i > 0 {
			result.close()
		}
		var err error
		if result, err = g.lookupOnce(r, timeout, threshold); err != nil {
			return nil, err
		}
		if result.matched() || result.definite() {
			break
		}
		select {
		case <-r.Context().Done():
			return result, nil
		default:
		}
	}
	return result, nil
}

// noMatch says why nothing could be picked: a certain miss, or substrates that did not answer.
func noMatch(result *lookupResult) error {
	if result.definite() {
		return platformerr.New(platformerr.NotFound, errors.New("no substrate match found"))
	}
	return platformerr.New(platformerr.NodeBusy, errors.New("no substrate answered in time"))
}

func (g *Gateway) handleHttp(w goHttp.ResponseWriter, r *goHttp.Request) error {
	result, err := g.lookup(r)
	if err != nil {
		return err
	}
	defer result.close()
	if !result.matched() {
		return noMatch(result)
	}
	matches := result.functions
	if len(result.websites) > len(result.functions) {
		matches = result.websites
	}
	sort.Slice(matches, func(i, j int) bool { return matches[j].metrics.Less(matches[i].metrics) })

	// Streams beyond the tries are dead weight on those nodes.
	tries := matches[:min(maxTries, len(matches))]
	if !tunnel.Replayable(r) {
		tries = tries[:1] // it can never be retried
	}
	for _, loser := range matches[len(tries):] {
		loser.Close()
	}

	for _, pick := range tries {
		w.Header().Set(ProxyHeader, pick.PID().String())
		if err = tunnel.Frontend(w, r, pick.Response); !errors.Is(err, tunnel.ErrRefused) {
			break
		}
	}

	switch {
	case err == nil:
		return nil
	case errors.Is(err, tunnel.ErrRefused):
		return platformerr.New(platformerr.NodeBusy, err)
	default:
		return fmt.Errorf("tunneling Frontend failed with: %w", err)
	}
}
