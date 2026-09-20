package cspbft

import "fmt"

// checkpoint.go is how the log stays bounded and how a replica that fell
// behind catches up.
//
// A checkpoint is the application's own digest of its state at a sequence
// number. This package never computes one — it asks, with TakeCheckpoint, and
// is answered with Checkpointed. When a quorum of replicas answer the same
// thing, the checkpoint is STABLE, and that single fact does four jobs at
// once: it moves the low water mark, it truncates the log, it becomes the
// proof a VIEW-CHANGE carries, and it is the one agreed state the rotation's
// epoch may be bound to.

type checkpointKey struct {
	seq    Sequence
	digest Digest
}

// Checkpointed is the caller's answer to TakeCheckpoint: the application state
// at seq digests to d.
func (r *Replica) Checkpointed(seq Sequence, d Digest) ([]Action, error) {
	r.started = true
	if r.broken != nil {
		return nil, r.broken
	}
	if seq == 0 {
		return nil, fmt.Errorf("%w: a checkpoint at sequence zero", ErrMalformed)
	}
	if seq <= r.lastStable {
		return nil, fmt.Errorf("%w: checkpoint %d is at or below the stable one, %d", ErrOutOfWatermarks, seq, r.lastStable)
	}
	cp := r.sign(Message{Kind: KindCheckpoint, From: r.self, Seq: seq, Digest: d})
	if r.broken != nil {
		return nil, r.broken
	}
	acts := []Action{Persist{Records: []Record{rec(cp)}}, Broadcast{Message: cp}}
	r.recordCheckpoint(cp)
	acts = append(acts, r.reviewStability(seq, d)...)
	return acts, nil
}

func (r *Replica) onCheckpoint(m Message) ([]Action, error) {
	// A checkpoint is the one message that is not bound to a view — states are
	// agreed across view changes — so it is bounded by sequence alone. The
	// upper bound is two log lengths ahead, which is as far as a replica can
	// legitimately be behind and still be talked to rather than transferred.
	if m.Seq <= r.lastStable {
		return nil, fmt.Errorf("%w: checkpoint %d is at or below the stable one, %d", ErrOutOfWatermarks, m.Seq, r.lastStable)
	}
	if m.Seq > r.lastStable+Sequence(4*r.k) {
		return nil, fmt.Errorf("%w: checkpoint %d is too far ahead of %d", ErrOutOfWatermarks, m.Seq, r.lastStable)
	}
	r.recordCheckpoint(m)
	// The q CHECKPOINTs that make a state stable ARE the proof a VIEW-CHANGE
	// carries, so a replica that restarted without them would send a
	// VIEW-CHANGE nobody can validate.
	acts := []Action{Persist{Records: []Record{rec(m)}}}
	return append(acts, r.reviewStability(m.Seq, m.Digest)...), nil
}

func (r *Replica) recordCheckpoint(m Message) {
	k := checkpointKey{m.Seq, m.Digest}
	if r.checkpoints[k] == nil {
		r.checkpoints[k] = map[ReplicaID]Message{}
	}
	r.checkpoints[k][m.From] = m
}

// reviewStability asks whether (seq, d) has just become stable.
func (r *Replica) reviewStability(seq Sequence, d Digest) []Action {
	if seq <= r.lastStable {
		return nil
	}
	votes := r.checkpoints[checkpointKey{seq, d}]
	if len(votes) < r.q {
		return nil
	}
	proof := make([]Message, 0, r.q)
	certifiers := make([]ReplicaID, 0, r.q)
	for _, id := range sortedIDs(votes) {
		if len(proof) == r.q {
			break
		}
		proof = append(proof, votes[id])
		certifiers = append(certifiers, id)
	}
	acts := r.adoptStable(seq, d, proof)
	if seq > r.lastExecuted {
		// A QUORUM CERTIFIED A STATE THIS REPLICA NEVER REACHED. It cannot
		// catch up by replaying the order — the log below the new low water
		// mark is gone everywhere — so it asks for the state itself.
		acts = append(acts, FetchState{Seq: seq, Digest: d, From: certifiers})
	}
	acts = append(acts, r.assignPending()...)
	return acts
}

// adoptStable installs a stable checkpoint: the durability record first,
// because everything after it discards state that cannot be recovered.
func (r *Replica) adoptStable(seq Sequence, d Digest, proof []Message) []Action {
	acts := []Action{Persist{Records: []Record{{Kind: RecordCheckpointStable, Seq: seq, Digest: d}}}}

	r.lastStable = seq
	r.stableDigest = d
	r.stableProof = proof
	r.log.discardThrough(seq)
	if r.lastAssigned < seq {
		r.lastAssigned = seq
	}
	r.gcCheckpoints(seq)
	r.gcRequests(seq)
	r.gcViewChanges()

	// THE ROTATION MOVES HERE AND NOWHERE ELSE. A stable checkpoint is the one
	// state a quorum has provably certified, so it is the only input on which
	// two correct replicas are guaranteed to compute the same permutation.
	if err := r.rot.recompute(seq, d); err != nil {
		acts = append(acts, Notice{Err: err})
	}
	return acts
}

func (r *Replica) gcCheckpoints(through Sequence) {
	for k := range r.checkpoints {
		if k.seq <= through {
			delete(r.checkpoints, k)
		}
	}
}

// gcRequests drops payloads whose execution is now covered by a stable
// checkpoint: a quorum holds the resulting state, so nobody will ever need to
// replay them.
func (r *Replica) gcRequests(through Sequence) {
	for d, seq := range r.executedAt {
		if seq > through {
			continue
		}
		delete(r.executedAt, d)
		delete(r.requests, d)
		delete(r.assigned, d)
		r.dropOrphans(d)
	}
	kept := r.arrival[:0]
	for _, d := range r.arrival {
		if _, ok := r.requests[d]; ok {
			kept = append(kept, d)
		}
	}
	r.arrival = kept
}

// gcViewChanges drops what belongs to a view this replica has left. The two
// "did I already" maps go with it: startViewChange and maybeSendNewView both
// refuse a view at or below the current one anyway, so a remembered yes about
// an older view answers a question nobody asks again — and this package bounds
// the log, the checkpoints and the held assignments, so two maps that grow one
// entry per view for the life of the process would be the exception with no
// reason behind it.
func (r *Replica) gcViewChanges() {
	for v := range r.viewChanges {
		if v < r.view {
			delete(r.viewChanges, v)
		}
	}
	for v := range r.sentViewChange {
		if v < r.view {
			delete(r.sentViewChange, v)
		}
	}
	for v := range r.sentNewView {
		if v < r.view {
			delete(r.sentNewView, v)
		}
	}
}

// Installed is the caller's answer to FetchState: the application now holds
// the state at seq, which digests to d.
func (r *Replica) Installed(seq Sequence, d Digest) ([]Action, error) {
	r.started = true
	if r.broken != nil {
		return nil, r.broken
	}
	if seq == r.lastStable && d != r.stableDigest {
		return nil, fmt.Errorf("%w: installed state at %d digests to something other than the checkpoint a quorum certified", ErrBadCertificate, seq)
	}
	if seq <= r.lastExecuted {
		return nil, nil
	}
	r.lastExecuted = seq
	r.log.discardThrough(seq)
	for dg, sq := range r.executedAt {
		if sq <= seq {
			delete(r.executedAt, dg)
		}
	}

	// A TRANSFER ENDS THIS REPLICA'S KNOWLEDGE OF WHAT IT WAS WAITING FOR, and
	// saying so is the only honest thing it can do. The transferred state
	// covers every request ordered at or below seq, but this replica never saw
	// those assignments, so it cannot tell which of the requests it is holding
	// are in there. Keeping them marked outstanding is the failure that
	// actually bites: the request timer can then never be cancelled, so the
	// replica forces a view change every timeout, forever, over a request that
	// was executed before it even arrived.
	//
	// What stays outstanding is exactly what is visibly still in flight: a
	// request named by a log entry above the transferred point. Anything else
	// is dropped, and a client that really is still waiting retransmits — which
	// is the client's job in this protocol and not a replica's.
	keep := map[Digest]bool{}
	for _, e := range r.log.entries {
		if e.seq > seq && !e.digest.IsNull() {
			keep[e.digest] = true
		}
	}
	for dg := range r.outstanding {
		if !keep[dg] {
			delete(r.outstanding, dg)
			delete(r.assigned, dg)
			r.dropOrphans(dg)
			if _, executed := r.executedAt[dg]; !executed {
				delete(r.requests, dg)
			}
		}
	}
	kept := r.arrival[:0]
	for _, d := range r.arrival {
		if _, ok := r.requests[d]; ok {
			kept = append(kept, d)
		}
	}
	r.arrival = kept

	return r.execute(), nil
}
