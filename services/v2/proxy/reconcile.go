package proxy

import (
	"context"
	"time"
)

// reconcile.go keeps what this node is listening for equal to what it holds.
//
// # It is a sweep and not an event
//
// A deployment arrives by a pointer moving, and nothing hands this service an
// edge to react to. So the shape is the one node/cron.go already uses: one
// step, reachable from a test with no goroutine and no clock, and a loop whose
// only job is to call it. A sweep also converges after a missed edge, which an
// event-driven listener table does not.
//
// # A node serves every proxy it holds, and no leader decides it
//
// This is the difference from a cron, where exactly one node in the fleet may
// fire. A listener on each node is what gives an SRV answer more than one
// target, so losing a node costs a client one retry rather than the service.
//
// # A proxy that stops being held stops listening
//
// Otherwise a node that dropped a deployment would keep a port open, keep
// reporting it, and keep accepting connections it would then refuse — which is
// worse than not answering, because DNS would go on sending clients to it.

// Deployments is what this node holds, in the terms Serve takes.
//
// services/worker answers it: what a node can serve is what it is already
// serving, because the register does not enumerate. A proxy in a deployment
// nothing has touched does not listen anywhere.
type Deployments interface {
	Proxies(ctx context.Context) []Bind
}

// DefaultSweep is how often this node compares what it serves to what it holds.
//
// A DEPLOY IS NOTICED WITHIN ONE SWEEP, which is the cost of having no edge to
// react to. Ten seconds is node/cron.go's tick for the same reason: it bounds
// the lateness of noticing and costs one map read per proxy.
const DefaultSweep = 10 * time.Second

// WithDeployments is where the list of proxies to serve comes from. A service
// without one serves only what Serve was called with by hand.
func WithDeployments(d Deployments) Option {
	return func(o *options) error {
		if d == nil {
			return ErrNoDeployments
		}
		o.deployments = d
		return nil
	}
}

// Sweep serves every proxy this node holds and stops the ones it no longer
// does. It reports how many listeners it started.
//
// A PROXY THAT CANNOT BE SERVED IS NOT RETRIED IN A TIGHTER LOOP. The next
// sweep is the retry, and the reason went to the operator the first time — a
// service that logged an unservable resource every ten seconds would bury the
// deploy that fixed it.
func (s *Service) Sweep(ctx context.Context) int {
	if s.opts.deployments == nil {
		return 0
	}

	held := s.opts.deployments.Proxies(ctx)

	wanted := make(map[string]Bind, len(held))
	for _, b := range held {
		wanted[b.Key()] = b
	}
	s.stopUnheld(wanted)

	var started int
	for key, b := range wanted {
		if s.holds(key) {
			// AN EDITED LIMIT TAKES EFFECT WITHIN ONE SWEEP. It is read here
			// rather than per connection because the accept path consults it
			// before it reads anything else — see limits.go.
			s.refreshLimits(ctx, key, b)
			continue
		}
		if _, err := s.Serve(ctx, b); err != nil {
			if !s.refused(key, err) {
				s.opts.warn("proxy: %s is not served: %s", key, err)
			}
			continue
		}
		s.forget(key)
		started++
	}
	return started
}

// Run sweeps until the context is done. It is the whole of this service's
// lifecycle: the listeners it opens are closed by Close.
func (s *Service) Run(ctx context.Context) {
	every := s.cfg.Sweep
	if every <= 0 {
		every = DefaultSweep
	}

	t := time.NewTicker(every)
	defer t.Stop()

	s.Sweep(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Sweep(ctx)
		}
	}
}

func (s *Service) holds(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.served[key]
	return ok
}

// stopUnheld closes the listeners for proxies this node no longer holds.
func (s *Service) stopUnheld(wanted map[string]Bind) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for key, b := range s.served {
		if _, still := wanted[key]; still {
			continue
		}
		b.listener.Close()
		delete(s.served, key)
		delete(s.refusals, key)
	}
}

// refused says whether this proxy has already been reported as unservable with
// this same reason, and records it if not.
//
// THE REASON IS PART OF THE KEY, so an author who fixes one refusal and creates
// another is told about the second — only a repeat of the same sentence about
// the same resource is silence.
func (s *Service) refused(key string, err error) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	said := err.Error()
	if s.refusals[key] == said {
		return true
	}
	s.refusals[key] = said
	return false
}

func (s *Service) forget(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.refusals, key)
}

// refreshLimits re-reads what one source may open on a proxy already being
// served. A deployment that cannot be read leaves the last limits in place,
// which is the safe direction: the alternative is dropping a ceiling because a
// register read failed.
func (s *Service) refreshLimits(ctx context.Context, key string, b Bind) {
	_, r, _, err := s.resource(ctx, b)
	if err != nil {
		return
	}

	lim := limitsOf(r)

	s.mu.Lock()
	defer s.mu.Unlock()
	if held, ok := s.served[key]; ok {
		held.limits = lim
	}
}
