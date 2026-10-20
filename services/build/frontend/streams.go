package frontend

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/taubyte/tau/pkg/git/repokey"
)

// streams.go is the SECOND SOURCE OF A PUSH, not a second pipeline. A forge
// with no webhook a signature covers (Gerrit) reports pushes on an event feed
// the deployment opens itself, over a channel it authenticated; a push read
// there is the same Push a verified delivery is, and goes the same way from
// here — refusal (push.go), then backend.Enqueue. Nothing about building
// differs by where the push came from.
//
// EVERY FRONTEND HOLDS EVERY STREAM, and that is safe because it is the
// webhook's own answer to a redelivery: the backend's job id is a hash of
// (repository, ref, commit), so N nodes enqueueing one push make one job. No
// node is elected to hold a stream, so no node's loss stops pushes arriving.
//
// A STREAM HAS NO REPLAY. Whatever happens while it is down is gone from it,
// so every time a subscription is accepted the branch heads of every linked
// repository on that forge are read and enqueued, and again every
// StreamResync while it stays up. A head already built is the job it already
// was (Enqueue is idempotent, and a finished job is not requeued), so the
// comparison against what was built is the backend's job table and costs one
// Enqueue per eligible head. The periodic pass also covers the two misses a
// reconnect cannot: the window between a subscription being accepted and the
// forge actually registering it, and a push whose Enqueue failed.

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
	ctx, cancel := context.WithTimeout(ctx, s.cfg.BackendTimeout)
	defer cancel()
	jobID, err := s.backend.Enqueue(ctx, p.Repository, p.Ref, p.Commit)
	if err != nil {
		s.log("stream %s: enqueue %s %s: %v", host, p.Repository, p.Commit, err)
		return
	}
	s.log("stream %s queued job %s for %s %s", host, jobID, p.Repository, p.Ref)
}
