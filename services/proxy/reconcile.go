package proxy

import (
	"context"
	"time"
)

// A deployment arrives by a pointer moving, with no event to react to, so this is a
// sweep: one step a test can call, and a loop that calls it. A sweep also converges
// after a missed change.
//
// Every node serves every proxy it holds, with no leader, so an SRV answer has more
// than one target. A proxy no longer held stops listening, or DNS would keep sending
// clients to a node that refuses them.

// Deployments is what this node holds, in the terms Serve takes. services/worker
// answers it, since the register does not enumerate; a proxy in a deployment nothing
// has touched does not listen anywhere.
type Deployments interface {
	Proxies(ctx context.Context) []Bind
}

// DefaultSweep is how often this node compares what it serves to what it holds,
// which bounds how late a deploy is noticed.
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
// does. It reports how many listeners it started. An unservable proxy is retried by
// the next sweep, and reported once per reason.
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
			// An edited limit takes effect within one sweep.
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

// refused says whether this proxy has already been reported as unservable with this
// same reason, and records it if not. A new reason is reported.
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
// served. A deployment that cannot be read leaves the last limits in place rather
// than dropping a ceiling.
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
