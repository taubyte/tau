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

	"github.com/taubyte/tau/pkg/membership"
	"github.com/taubyte/tau/pkg/placement"
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

	// members is the PLACEMENT view, which runs on a longer timescale than the
	// routing one — see beat. Nil only in a service composed without one.
	members *membership.Set

	// regionMu guards regions, which is where a peer's region is kept for as
	// long as MEMBERSHIP remembers it rather than as long as the routing view
	// does. The two forget on different timescales — routing drops a row at 20
	// intervals, placement keeps a peer far longer — and reading the region out
	// of the routing view meant a settled node lost its region while it was
	// still in the placement set, landing under "" as if it had never said.
	regionMu sync.Mutex
	regions  map[string]string

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

	// THE PLACEMENT VIEW'S LIVENESS IS THIS SERVICE'S FRESHNESS, because both
	// answer "has this peer reported lately" and there is only one heartbeat.
	//
	// STABILIZE IS READ AND NOT DERIVED HERE — withDefaults derives it, floors
	// it and refuses one shorter than the freshness window, so the number an
	// operator can write and the number this service runs on are one number.
	members, err := membership.New(membership.Options{
		Liveness: c.Fresh, Stabilize: c.Stabilize, Now: o.now,
	})
	if err != nil {
		return nil, err
	}

	return &Service{
		cfg:         c,
		opts:        o,
		peer:        h.ID().String(),
		view:        view,
		members:     members,
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

				FleetRevision:    r.GetFleetRevision(),
				DatabasesPending: int(r.GetDatabasesPending()),
			})
			s.beat(r.GetPeer(), r.GetIncarnation(), r.GetReady(), r.GetRegion())
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

		// THESE DO come from the snapshot: they are what this node's own
		// services answer about themselves, and they change every tick.
		FleetRevision:    snap.FleetRevision,
		DatabasesPending: uint32(snap.DatabasesPending),
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

		FleetRevision:    snap.FleetRevision,
		DatabasesPending: snap.DatabasesPending,
	})
	s.beat(s.peer, s.incarnation, snap.Ready, s.cfg.Region)

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

// beat feeds the placement view, which runs on a different timescale from the
// routing view and deliberately so.
//
// ROUTING WANTS FRESH; PLACEMENT WANTS SETTLED. A node that blinks for one
// interval should stop receiving requests immediately and must NOT move a
// database — moving one costs a copy handed over and a copy deleted, and doing
// that on a blip is how a fleet spends its day rebalancing. pkg/membership is
// written for exactly this split: Live is the acknowledgement target, Stable is
// "who owns data", and it requires continuous presence in both directions.
func (s *Service) beat(peer string, incarnation uint64, accepts bool, region string) {
	if s.members == nil || peer == "" {
		return
	}
	// Observe refuses a stale run on its own, the same way the view does.
	s.members.Observe(membership.Beat{Peer: peer, Incarnation: incarnation, Accepts: accepts})

	s.regionMu.Lock()
	defer s.regionMu.Unlock()
	if s.regions == nil {
		s.regions = map[string]string{}
	}
	s.regions[peer] = region
}

// Placement is the member set a placement runs over, grouped by the region each
// node reports.
//
// IT IS THE STABLE SET AND NOT THE FRESH ONE — see beat. View.ByRegion answers
// the routing question and is the wrong input here: it would move data on a
// blip.
//
// A STABLE MEMBER THIS NODE HAS NO REGION FOR IS STILL A MEMBER, under "" — the
// key for "said nothing", which is never a region name. Dropping it would make a
// node invisible to placement entirely, which is worse than placing it in the
// bucket for nodes that will not say where they are.
func (s *Service) Placement() map[string][]placement.Member {
	if s.members == nil {
		return nil
	}

	s.regionMu.Lock()
	defer s.regionMu.Unlock()

	out := map[string][]placement.Member{}
	known := map[string]bool{}
	for _, m := range s.members.StablePlacement() {
		at := s.regions[m.ID]
		out[at] = append(out[at], m)
		known[m.ID] = true
	}

	// FORGOTTEN BY MEMBERSHIP IS FORGOTTEN HERE, so a fleet that has churned
	// does not carry a region for every peer it has ever heard from.
	for peer := range s.regions {
		if !known[peer] {
			if _, remembered := s.members.Get(peer); !remembered {
				delete(s.regions, peer)
			}
		}
	}
	return out
}

// Pending is how many databases the whole fleet knows to be below their floor,
// summed over the nodes whose reports are current.
//
// SUMMED AND NOT MAXED, because a database short in two places is two problems:
// each holder counts what IT cannot confirm, so the total is the amount of
// repair outstanding rather than the number of databases involved.
//
// It is a number an operator watches, not one anything acts on — a node acts on
// its own copies, which it can see exactly.
func (s *Service) Pending(maxAge time.Duration) int {
	total := 0
	for _, n := range s.view.ByRegion(maxAge) {
		for _, node := range n {
			total += node.DatabasesPending
		}
	}
	return total
}
