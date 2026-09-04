package backend

import "github.com/taubyte/tau/services/build"

// EVERY RPC IN THIS SERVICE IS LEADER-ONLY, READS INCLUDED, and it is one rule
// rather than five.
//
// THE WRITES HAVE NO CHOICE. pkg/queue refuses Push, Claim, Ack, Nack and
// Sweep off the leader and deliberately does not retry — "a follower therefore
// consumes nothing" — and its ErrNotLeader IS raft.ErrNotLeader.
//
// THE READS ARE THE INTERESTING HALF. pkg/raft's Barrier is hashicorp/raft's,
// which is leader-only, so a follower cannot make a linearizable read AT ALL:
// its Get serves local committed state that can lag. A Get answering NotFound
// for a job enqueued a second ago is a user-visible lie, and the caller has to
// loop for the writes anyway, so the honest answer is the same one — ask
// somebody else.
//
// CodeUnavailable is the wire spelling of that, and services/build/clients'
// Pool.Do already treats it that way for a peer that is down or does not serve
// the protocol. A follower is the third case, not a new branch — which is why
// there is no Leader() on Store, no forwarding, and no client cache keyed by
// leadership anywhere in this package.
func (s *Service) lead() error {
	if !s.store.IsLeader() {
		return errUnavailable("not the leader of %s", build.QueueDomain)
	}
	// The barrier is load-bearing rather than defensive, for the same reason
	// queue.prepare() needs one: a node that has just become leader can hold
	// committed entries it has not applied, so an Enqueue that read the job
	// table without it would mint a second job for a push it already has.
	if err := s.store.Barrier(s.cfg.Queue.ApplyTimeout); err != nil {
		return errUnavailable("barrier on %s: %v", build.QueueDomain, err)
	}
	return nil
}
