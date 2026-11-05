package gateway

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
	"time"

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
				writeError(w, err)
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

// lookupTimeouts is how long each attempt waits for substrates to answer. The
// first is short because an attempt waits out its timeout looking for more
// peers, and it is enough for an idle node with a cached match. A busy node or a
// cold lookup after a deploy needs longer, and that must not read as "no match".
var lookupTimeouts = []time.Duration{0, 200 * time.Millisecond, 2 * time.Second}

type lookupResult struct {
	websites, functions []wrappedResponse
	discard             []*client.Response
	answered, timedOut  bool // a substrate said it has no match; one did not answer in time
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

// definite says a further attempt cannot change the answer: every substrate
// that was asked answered that it has no match.
func (l *lookupResult) definite() bool { return l.answered && !l.timedOut }

func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "deadline") || strings.Contains(msg, "timeout") || strings.Contains(msg, "timed out")
}

func (g *Gateway) lookupOnce(r *goHttp.Request, timeout time.Duration) (*lookupResult, error) {
	var ops []client.Option[client.Request]
	if timeout > 0 {
		ops = append(ops, client.Timeout(timeout))
	}

	resCh, err := g.substrateClient.ProxyHTTP(r.Host, r.URL.Path, r.Method, ops...)
	if err != nil {
		return nil, fmt.Errorf("substrate client proxyHttp failed with: %w", err)
	}

	result := new(lookupResult)
	for response := range resCh {
		if err := response.Error(); err != nil {
			logger.Debugf("response from node `%s` failed with: %s", response.PID().String(), err.Error())
			if isTimeout(err) {
				result.timedOut = true
			} else {
				result.answered = true
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
	var result *lookupResult
	for i, timeout := range lookupTimeouts {
		if i > 0 {
			result.close()
		}
		var err error
		if result, err = g.lookupOnce(r, timeout); err != nil {
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

// noMatchError carries the status for a request nothing could be picked for.
type noMatchError struct {
	status  int
	message string
}

func (e *noMatchError) Error() string { return e.message }

func noMatch(result *lookupResult) error {
	if result.definite() {
		return &noMatchError{goHttp.StatusNotFound, "no substrate match found"}
	}
	return &noMatchError{goHttp.StatusServiceUnavailable, "no substrate answered in time"}
}

func writeError(w goHttp.ResponseWriter, err error) {
	status := goHttp.StatusInternalServerError
	var miss *noMatchError
	if errors.As(err, &miss) {
		status = miss.status
		if status == goHttp.StatusServiceUnavailable {
			w.Header().Set("Retry-After", "1")
		}
	}
	w.WriteHeader(status)
	w.Write([]byte(err.Error()))
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
	websiteMatches, funcMatches := result.websites, result.functions

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
