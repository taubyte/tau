// Package proxy is the ingress: it takes a client request, decides which node
// should serve it, and forwards it there.
//
// # Where it sits
//
//	client -> dns (which node?) -> THIS -> p2p -> the node running the workload
//	                                              -> its local container port
//
// A workload's own port never leaves the node it runs on. pkg/invoke/container
// allocates a host port and tells the container about it; nothing outside that
// machine ever dials it. What crosses the network is a p2p stream to the node's
// proxy, and the proxy is what knows how to reach the workload locally.
//
// # It routes from measurements, not from a table
//
// The decision is a stats read: who is serving this name, no older than a
// tolerance this package states, least loaded first. That is the same read dns
// makes, through the same one-method interface, so the two cannot drift into
// disagreeing about who is up.
//
// # The three rules that make forwarding safe
//
// A RETRY ONLY EVER FOLLOWS A FAILURE TO CONNECT. Once a request has been
// written to a node, retrying it somewhere else risks running a non-idempotent
// operation twice, and this package cannot know which requests those are. A
// connection that was never established carries no such risk, so that — and
// only that — is retried.
//
// THE LOCAL NODE IS TRIED WITHOUT A NETWORK HOP. If this node is in the answer,
// it is the answer: going out over p2p to reach a workload on the same machine
// is latency bought for nothing. It is not FAVOURED — the ordering by load
// still decides — it just is not dialled remotely when it wins.
//
// NOTHING SERVING IS 503, NEVER 404. A name whose nodes are all restarting
// exists and is momentarily unserved, and 404 tells a client — and every cache
// between here and it — that the thing is gone. The same reasoning dns applies
// to NXDOMAIN.
package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/taubyte/tau/pkg/stats"
)

// Router is the slice of stats this service needs.
//
// It is the same shape services/dns declares, deliberately: dns and the proxy
// answer the same question — who is serving this name — and two spellings of
// one question is how two answers start.
type Router interface {
	Route(name string, maxAge time.Duration) []stats.Node
}

// Forwarder opens a round trip to one node. Production dials a p2p stream;
// tests hand back an http.RoundTripper over a local server, which is what lets
// every rule above be a unit test.
type Forwarder interface {
	// To returns a round-tripper that reaches this node's proxy. An error means
	// the connection could not be established — the one failure this package
	// retries.
	To(node stats.Node) (http.RoundTripper, error)
}

// ForwarderFunc adapts a function to Forwarder.
type ForwarderFunc func(stats.Node) (http.RoundTripper, error)

func (f ForwarderFunc) To(n stats.Node) (http.RoundTripper, error) { return f(n) }

// Config is what an operator declared.
type Config struct {
	// MaxAge is how stale a stats row may be and still be forwarded to.
	// Default 15s.
	//
	// IT IS SHORTER THAN DNS'S. A DNS answer is cached for its TTL and acted on
	// later; this decision is made and used immediately, so it can afford to
	// insist on a fresher view — and it should, because forwarding to a node
	// that has gone is a failed request rather than a stale lookup.
	MaxAge time.Duration

	// MaxAttempts is how many nodes are tried before giving up. Default 3.
	//
	// Only connection failures consume an attempt; see the package doc.
	MaxAttempts int

	// Timeout bounds one forwarded request. Default 30s.
	Timeout time.Duration
}

// Defaults.
const (
	DefaultMaxAge      = 15 * time.Second
	DefaultMaxAttempts = 3
	DefaultTimeout     = 30 * time.Second
)

var (
	ErrNoRouter    = errors.New("proxy: no router to decide where a request goes")
	ErrNoForwarder = errors.New("proxy: no forwarder to reach a node with")
	ErrNoTarget    = errors.New("proxy: nothing is serving this name")
	ErrUnreachable = errors.New("proxy: no node could be reached")
)

func (c Config) withDefaults() (Config, error) {
	if c.MaxAge == 0 {
		c.MaxAge = DefaultMaxAge
	}
	if c.MaxAttempts == 0 {
		c.MaxAttempts = DefaultMaxAttempts
	}
	if c.Timeout == 0 {
		c.Timeout = DefaultTimeout
	}
	if c.MaxAge <= 0 || c.MaxAttempts < 1 || c.Timeout <= 0 {
		return Config{}, fmt.Errorf("proxy: max age %s, %d attempts and timeout %s all need positive budgets",
			c.MaxAge, c.MaxAttempts, c.Timeout)
	}
	return c, nil
}

// Option injects what this service is built from.
type Option func(*options) error

type options struct {
	router    Router
	forwarder Forwarder
	self      string
	local     http.RoundTripper
	logf      func(format string, args ...any)
}

// WithRouter is where routing decisions come from. Required.
func WithRouter(r Router) Option {
	return func(o *options) error {
		if r == nil {
			return ErrNoRouter
		}
		o.router = r
		return nil
	}
}

// WithForwarder is how another node is reached. Required.
func WithForwarder(f Forwarder) Option {
	return func(o *options) error {
		if f == nil {
			return ErrNoForwarder
		}
		o.forwarder = f
		return nil
	}
}

// WithLocal says which peer THIS node is and how to reach a workload on it
// without a network hop.
//
// Optional, and its absence is a working proxy: a pure edge node that runs no
// workloads has nothing local to shortcut to. Passing one without the other is
// refused, because a peer id with no way to serve it would silently route every
// local request into nothing.
func WithLocal(peer string, rt http.RoundTripper) Option {
	return func(o *options) error {
		if peer == "" || rt == nil {
			return errors.New("proxy: a local shortcut needs both a peer id and a way to serve it")
		}
		o.self = peer
		o.local = rt
		return nil
	}
}

// WithLogger replaces where this service says what it could not reach.
func WithLogger(logf func(format string, args ...any)) Option {
	return func(o *options) error {
		if logf == nil {
			return errors.New("proxy: logger must not be nil")
		}
		o.logf = logf
		return nil
	}
}

// Service forwards requests to whichever node should serve them.
type Service struct {
	cfg  Config
	opts options
}

// New builds the service.
func New(cfg Config, opts ...Option) (*Service, error) {
	effective, err := cfg.withDefaults()
	if err != nil {
		return nil, err
	}
	o := options{logf: func(string, ...any) {}}
	for _, apply := range opts {
		if err := apply(&o); err != nil {
			return nil, err
		}
	}
	if o.router == nil {
		return nil, ErrNoRouter
	}
	if o.forwarder == nil {
		return nil, ErrNoForwarder
	}
	return &Service{cfg: effective, opts: o}, nil
}

// ServeHTTP is the ingress.
func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := hostname(r.Host)
	if name == "" {
		http.Error(w, "no host", http.StatusBadRequest)
		return
	}

	nodes := s.opts.router.Route(name, s.cfg.MaxAge)
	if len(nodes) == 0 {
		// 503 and never 404: see the package doc. A name whose nodes are all
		// restarting exists and is momentarily unserved.
		s.opts.logf("proxy: nothing serving %q", name)
		http.Error(w, ErrNoTarget.Error(), http.StatusServiceUnavailable)
		return
	}

	resp, err := s.forward(r, nodes)
	if err != nil {
		s.opts.logf("proxy: %q: %v", name, err)
		http.Error(w, ErrUnreachable.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// forward tries nodes in order until one answers or the attempts run out.
//
// A NODE THAT ANSWERS ENDS THE LOOP WHATEVER IT ANSWERED. A 500 from the
// workload is the workload's answer and belongs to the client; retrying it
// elsewhere would run the request twice and call the second result the truth.
func (s *Service) forward(r *http.Request, nodes []stats.Node) (*http.Response, error) {
	var attempted int
	var lastErr error

	for _, n := range nodes {
		if attempted >= s.cfg.MaxAttempts {
			break
		}
		attempted++

		rt, err := s.transportFor(n)
		if err != nil {
			// Could not connect: nothing was sent, so the next node is safe.
			lastErr = fmt.Errorf("%s: %w", n.Peer, err)
			continue
		}

		ctx, cancel := contextWithTimeout(r, s.cfg.Timeout)
		resp, err := rt.RoundTrip(r.Clone(ctx))
		if err != nil {
			cancel()
			lastErr = fmt.Errorf("%s: %w", n.Peer, err)
			continue
		}
		// The body is still being read when this returns, so the cancel goes
		// with it rather than firing here.
		resp.Body = &bodyWithCancel{ReadCloser: resp.Body, cancel: cancel}
		return resp, nil
	}

	if lastErr == nil {
		lastErr = ErrNoTarget
	}
	return nil, lastErr
}

// transportFor is the local shortcut or a forwarded connection.
func (s *Service) transportFor(n stats.Node) (http.RoundTripper, error) {
	if s.opts.local != nil && n.Peer == s.opts.self {
		return s.opts.local, nil
	}
	return s.opts.forwarder.To(n)
}

// hostname strips the port a client may have sent, and lowercases, so the name
// matches what a workload is deployed under.
func hostname(host string) string {
	h := host
	if i := strings.LastIndex(h, ":"); i >= 0 && !strings.Contains(h[i:], "]") {
		h = h[:i]
	}
	return strings.ToLower(strings.Trim(h, "[]"))
}

// contextWithTimeout bounds one forwarded request without discarding whatever
// deadline the client already imposed.
func contextWithTimeout(r *http.Request, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), d)
}

// bodyWithCancel releases the request's context when the caller is done reading
// the response.
//
// CANCELLING WHEN RoundTrip RETURNS WOULD TRUNCATE THE BODY. The response is
// still streaming at that point, and a cancelled context closes it under the
// reader — which looks to a client like a short read rather than a cancellation.
type bodyWithCancel struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *bodyWithCancel) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}
