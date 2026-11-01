package cspbft

import "fmt"

// A checkpoint is the application's digest of its state at a sequence number, asked
// for with TakeCheckpoint and answered with Checkpointed. Once a quorum agree it is
// stable, which moves the low water mark, truncates the log, becomes a VIEW-CHANGE's
// proof, and is the one agreed state the rotation's epoch binds to.

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
	// A checkpoint is not bound to a view, so it is bounded by sequence alone, up to two
	// log lengths ahead.
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

// gcViewChanges drops what belongs to a view this replica has left, including the two
// "did I already" maps, which would otherwise grow one entry per view forever.
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

	// After a transfer this replica cannot tell which held requests the transferred
	// state already covers; kept outstanding, one would force a view change every
	// timeout forever. Only requests named by a log entry above seq stay outstanding; a
	// client still waiting retransmits.
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
