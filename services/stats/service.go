// Package stats collects what every node says about itself, so dns and gateway
// can route from measurements instead of guesses.
//
// It is one half of v1's seer; dns is the other. The split is not tidiness: the
// two halves have different shapes at scale (answering a query is read-mostly
// and latency-bound, collecting usage is write-heavy and staleness-tolerant)
// and different trust properties, which pkg/stats' doc argues in full.
//
// THIS PACKAGE IS THE NETWORK HALF AND NOTHING ELSE. Every decision about what
// a report means — how old is too old, who wins a tie, whether a saturated node
// is still an answer — lives in pkg/stats, which has no host, no bus and no
// goroutine in it. What is here is a ticker, an encoder and a subscription, so
// that the interesting rules stay unit-testable and this file stays boring.
package stats

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"google.golang.org/protobuf/proto"

	statsv1 "github.com/taubyte/tau/pkg/proto/gen/tau/stats/v1"
	"github.com/taubyte/tau/pkg/stats"
)

// Service is a node's stats participation: it broadcasts this node's own
// numbers and keeps a view of everyone else's.
type Service struct {
	cfg  Config
	opts options
	peer string
	view *stats.View

	// incarnation distinguishes this run from the last. It is chosen once, at
	// construction, and never lowered — see the proto's field comment.
	incarnation uint64

	mu      sync.Mutex
	stopped bool
	cancel  context.CancelFunc
	done    chan struct{}
}

// New builds the service. It does not start it — Start does, so a caller can
// construct, inspect and then decide.
//
// It takes a host.Host and never a peer.Node: the host is here for one thing,
// this node's identity, and taking the bundle to get an id is what makes dream
// parity a retrofit instead of an adapter.
func New(h host.Host, cfg Config, opts ...Option) (*Service, error) {
	if h == nil {
		return nil, errors.New("stats: a service with no host does not know who it is reporting as")
	}
	c, err := cfg.withDefaults()
	if err != nil {
		return nil, err
	}

	var o options
	for _, apply := range opts {
		if err := apply(&o); err != nil {
			return nil, err
		}
	}
	if o.bus == nil {
		return nil, ErrNoBus
	}
	if o.reporter == nil {
		return nil, ErrNoReporter
	}
	if o.now == nil {
		o.now = time.Now
	}

	view, err := stats.New(c.viewOptions(o.now))
	if err != nil {
		return nil, err
	}

	return &Service{
		cfg:         c,
		opts:        o,
		peer:        h.ID().String(),
		view:        view,
		incarnation: uint64(o.now().UnixNano()),
		done:        make(chan struct{}),
	}, nil
}

// Start begins broadcasting and listening. It returns once both are running.
func (s *Service) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return errors.New("stats: this service has been closed")
	}
	if s.cancel != nil {
		s.mu.Unlock()
		return errors.New("stats: already started")
	}
	ctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.mu.Unlock()

	reports, err := s.opts.bus.Subscribe(ctx)
	if err != nil {
		cancel()
		s.mu.Lock()
		s.cancel = nil
		s.mu.Unlock()
		return fmt.Errorf("stats: subscribing: %w", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); s.listen(ctx, reports) }()
	go func() { defer wg.Done(); s.broadcast(ctx) }()
	go func() { wg.Wait(); close(s.done) }()

	return nil
}

// Close stops the service and waits for it to finish. It is safe to call twice.
func (s *Service) Close() error {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return nil
	}
	s.stopped = true
	cancel := s.cancel
	s.mu.Unlock()

	if cancel == nil {
		return nil // never started
	}
	cancel()
	<-s.done
	return nil
}

// listen folds every report that arrives into the view.
//
// A REPORT THAT DOES NOT DECODE IS DROPPED AND THE LOOP CONTINUES. The bus is
// a broadcast medium: one malformed message must not be able to stop a node
// from hearing every subsequent one, which is what returning here would do.
func (s *Service) listen(ctx context.Context, reports <-chan []byte) {
	for {
		select {
		case <-ctx.Done():
			return
		case b, ok := <-reports:
			if !ok {
				return
			}
			var r statsv1.Report
			if err := proto.Unmarshal(b, &r); err != nil {
				continue
			}
			// Observe refuses a bad peer, a bad load and an older
			// incarnation. Each refusal is about the sender, not about this
			// node, so there is nothing here to do but keep listening.
			s.view.Observe(stats.Report{
				Peer:        r.GetPeer(),
				Incarnation: r.GetIncarnation(),
				Load:        r.GetLoad(),
				Serving:     r.GetServing(),
				Ready:       r.GetReady(),
				Addresses:   r.GetAddresses(),
				Sockets:     socketsOf(r.GetSockets()),
				Region:      r.GetRegion(),
			})
		}
	}
}

// broadcast sends this node's own report on every tick, and once immediately so
// a fresh node is routable before the first interval elapses rather than after
// it.
func (s *Service) broadcast(ctx context.Context) {
	t := time.NewTicker(s.cfg.Interval)
	defer t.Stop()

	s.report(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.report(ctx)
		}
	}
}

// report asks this node about itself and broadcasts the answer.
//
// A FAILURE HERE IS NOT FATAL AND IS NOT RETRIED. The next tick is along
// shortly, and every reader already knows how old the row it holds is — that is
// what Age is for. Retrying inside a tick would only make a struggling node
// send more.
func (s *Service) report(ctx context.Context) {
	snap, err := s.opts.reporter.Snapshot(ctx)
	if err != nil {
		return
	}
	if snap.Load < 0 || snap.Load > 1 {
		// Refused rather than clamped: 1.4 clamped to 1 hides a broken
		// measurement behind a plausible number, and this node would then be
		// sorted as merely saturated rather than as broken.
		return
	}

	b, err := proto.Marshal(&statsv1.Report{
		Peer:        s.peer,
		Incarnation: s.incarnation,
		Load:        snap.Load,
		Serving:     snap.Serving,
		Ready:       snap.Ready,
		Addresses:   snap.Addresses,
		Sockets:     wireSockets(snap.Sockets),

		// NOT FROM THE SNAPSHOT. Where a node is, is what an operator
		// configured, not something it measures each tick — so it sits beside
		// the peer id rather than travelling with the load.
		Region: s.cfg.Region,
	})
	if err != nil {
		return
	}

	// This node folds its own report in directly rather than waiting to hear
	// it back. Whether a bus echoes to its own publisher is a property of the
	// bus, and a node that could not see itself would be missing from its own
	// routing answers on any bus that does not.
	s.view.Observe(stats.Report{
		Peer:        s.peer,
		Incarnation: s.incarnation,
		Load:        snap.Load,
		Serving:     snap.Serving,
		Ready:       snap.Ready,
		Addresses:   snap.Addresses,
		Sockets:     snap.Sockets,
		Region:      s.cfg.Region,
	})

	s.opts.bus.Publish(ctx, b)
}

// Peer is this node's identity, as it reports it.
func (s *Service) Peer() string { return s.peer }

// Route is the read dns and gateway make: who can answer for this name, no
// older than maxAge, least loaded first. The caller states the tolerance — see
// pkg/stats for why there is no default.
func (s *Service) Route(name string, maxAge time.Duration) []stats.Node {
	return s.view.Route(name, maxAge)
}

// Fresh is every node whose report arrived within the configured window.
func (s *Service) Fresh() []stats.Node { return s.view.Fresh() }

// All is every remembered node, at any age.
func (s *Service) All() []stats.Node { return s.view.All() }

// Get is one node's row, however old.
func (s *Service) Get(peer string) (stats.Node, bool) { return s.view.Get(peer) }

// socketsOf and wireSockets convert one message per direction.
//
// A PORT THAT DOES NOT FIT IS DROPPED rather than truncated. The wire carries
// uint32 because proto3 has no uint16, so a sender that put 70000 there would
// otherwise become port 4464 on every reader — a routable-looking answer to a
// listener nobody has.
func socketsOf(in []*statsv1.Socket) []stats.Socket {
	out := make([]stats.Socket, 0, len(in))
	for _, s := range in {
		if s.GetPort() == 0 || s.GetPort() > math.MaxUint16 {
			continue
		}
		out = append(out, stats.Socket{
			Host:      s.GetHost(),
			Protocol:  s.GetProtocol(),
			Transport: s.GetTransport(),
			Port:      uint16(s.GetPort()),
		})
	}
	return out
}

func wireSockets(in []stats.Socket) []*statsv1.Socket {
	out := make([]*statsv1.Socket, 0, len(in))
	for _, s := range in {
		out = append(out, &statsv1.Socket{
			Host:      s.Host,
			Protocol:  s.Protocol,
			Transport: s.Transport,
			Port:      uint32(s.Port),
		})
	}
	return out
}
