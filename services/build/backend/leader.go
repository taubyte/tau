package backend

import "github.com/taubyte/tau/services/build"

// lead enforces that every RPC is leader-only, reads included. pkg/queue refuses
// writes off the leader, and pkg/raft's Barrier is leader-only, so a follower
// cannot read linearizably; a stale NotFound for a just-enqueued job would be a
// lie. Unavailable sends the caller's Pool.Do to the next peer.
func (s *server) lead() error {
	if !s.store.IsLeader() {
		return errUnavailable("not the leader of %s", build.QueueDomain)
	}
	// A new leader may hold committed but unapplied entries; without the barrier
	// Enqueue could mint a duplicate job.
	if err := s.store.Barrier(s.cfg.Queue.ApplyTimeout); err != nil {
		return errUnavailable("barrier on %s: %v", build.QueueDomain, err)
	}
	return nil
}
