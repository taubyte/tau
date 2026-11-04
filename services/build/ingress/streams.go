package ingress

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/taubyte/tau/pkg/git/repokey"
)

// streams.go is a second source of pushes, not a second pipeline: a push from a
// forge's event feed (Gerrit) goes the webhook's way. Every build node holds every
// stream; the job id dedups. A stream has no replay, so on every accepted
// subscription and every StreamResync the eligible heads of every linked
// repository are enqueued again, which is idempotent.

// startStreams starts one subscription loop per forge host with a feed. It is
// a no-op without WithStreams.
func (s *Service) startStreams(ctx context.Context) {
	if s.streams == nil {
		return
	}
	ctx, s.stopStreams = context.WithCancel(ctx)
	for _, host := range s.streams.Hosts() {
		s.streaming.Add(1)
		go func() {
			defer s.streaming.Done()
			s.follow(ctx, host)
		}()
	}
}

// follow holds host's stream open until ctx ends: subscribe, and on a break
// wait and subscribe again, the wait doubling per failure up to
// StreamBackoffMax and starting over once a subscription is accepted.
func (s *Service) follow(ctx context.Context, host string) {
	backoff := s.cfg.StreamBackoff
	for {
		var (
			resyncs  sync.WaitGroup
			accepted atomic.Bool
		)
		conn, cancel := context.WithCancel(ctx)
		err := s.streams.Stream(conn, host, func() {
			accepted.Store(true)
			resyncs.Add(1)
			go func() {
				defer resyncs.Done()
				s.resync(conn, host)
			}()
		}, func(p Push, err error) {
			if err != nil {
				s.log("stream %s: refused a claim: %v", host, err)
				return
			}
			s.streamed(ctx, host, p)
		})
		cancel()
		resyncs.Wait()
		if ctx.Err() != nil {
			return
		}
		if accepted.Load() {
			backoff = s.cfg.StreamBackoff
		}
		s.log("stream %s: %v; subscribing again in %s", host, err, backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(2*backoff, s.cfg.StreamBackoffMax)
	}
}

// resync compares heads now and every StreamResync until the subscription
// ends.
func (s *Service) resync(ctx context.Context, host string) {
	tick := time.NewTicker(s.cfg.StreamResync)
	defer tick.Stop()
	for {
		s.reconcile(ctx, host)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// reconcile enqueues the eligible branch heads of every linked repository on
// host. A repository whose heads cannot be read is logged and skipped, so one
// cannot hide the others.
func (s *Service) reconcile(ctx context.Context, host string) {
	repos, err := s.linked.LinkedRepositories(ctx)
	if err != nil {
		s.log("stream %s: listing linked repositories: %v", host, err)
		return
	}
	seen := make(map[string]bool, len(repos))
	for _, spelling := range repos {
		key, err := repokey.Parse("https://" + spelling)
		if err != nil || key.Host != host || seen[key.String()] {
			continue
		}
		seen[key.String()] = true
		heads, err := s.streams.Heads(ctx, key.String())
		if err != nil {
			s.log("stream %s: reading the heads of %s: %v", host, key, err)
			continue
		}
		for _, p := range heads {
			s.streamed(ctx, host, p)
		}
	}
}

// streamed is a push from a feed taken the rest of the way a verified
// delivery's is.
func (s *Service) streamed(ctx context.Context, host string, p Push) {
	if err := s.refusal(p); err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, s.cfg.CallTimeout)
	defer cancel()
	jobID, err := s.jobs.Enqueue(ctx, p.Repository, p.Ref, p.Commit)
	if err != nil {
		s.log("stream %s: enqueue %s %s: %v", host, p.Repository, p.Commit, err)
		return
	}
	s.log("stream %s queued job %s for %s %s", host, jobID, p.Repository, p.Ref)
}
