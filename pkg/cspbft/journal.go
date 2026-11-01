package cspbft

import (
	"encoding/binary"
	"fmt"
)

// This package persists nothing: it emits Persist actions, durable before any later
// action in the same slice, so a restarted replica cannot contradict a vote it sent.
// A record is the signed message itself in preimage.go's layout, except the two local
// facts: the view entered and the checkpoint made stable.

type RecordKind uint8

const (
	// RecordMessage is a protocol message this replica accepted or sent. It is
	// the whole signed message, so a restored replica can put it back in a
	// VIEW-CHANGE and have it verify elsewhere.
	RecordMessage RecordKind = iota + 1

	// RecordViewEntered is this replica moving to a view. Without it, a
	// restarted replica could re-enter an older view and vote there again.
	RecordViewEntered

	// RecordCheckpointStable is a checkpoint becoming stable, which is what
	// moves the low water mark and the rotation's epoch.
	RecordCheckpointStable
)

type Record struct {
	Kind    RecordKind
	Message Message  // RecordMessage
	View    View     // RecordViewEntered
	Seq     Sequence // RecordCheckpointStable
	Digest  Digest   // RecordCheckpointStable
}

// Encode is the record as bytes, for a caller that wants a journal file rather
// than a table. The caller is free to ignore it and store the struct.
func (rec Record) Encode() []byte {
	b := make([]byte, 0, 128)
	b = append(b, recordDomain...)
	b = append(b, byte(rec.Kind))
	b = binary.BigEndian.AppendUint64(b, uint64(rec.View))
	b = binary.BigEndian.AppendUint64(b, uint64(rec.Seq))
	b = append(b, rec.Digest[:]...)
	inner := appendMessage(nil, rec.Message, true)
	return appendBytes(b, inner)
}

// DecodeRecord is Encode's inverse. A record that does not decode is
// ErrCorruptRecord and must not be skipped: the record that is missing may be
// the one that says what this replica already voted for.
func DecodeRecord(b []byte) (Record, error) {
	var rec Record
	if len(b) < len(recordDomain) || string(b[:len(recordDomain)]) != recordDomain {
		return rec, fmt.Errorf("%w: wrong domain prefix", ErrCorruptRecord)
	}
	r := &reader{b: b, i: len(recordDomain)}
	k, err := r.byte()
	if err != nil {
		return rec, fmt.Errorf("%w: %w", ErrCorruptRecord, err)
	}
	rec.Kind = RecordKind(k)
	v, err := r.u64()
	if err != nil {
		return rec, fmt.Errorf("%w: %w", ErrCorruptRecord, err)
	}
	rec.View = View(v)
	s, err := r.u64()
	if err != nil {
		return rec, fmt.Errorf("%w: %w", ErrCorruptRecord, err)
	}
	rec.Seq = Sequence(s)
	if rec.Digest, err = r.digest(); err != nil {
		return rec, fmt.Errorf("%w: %w", ErrCorruptRecord, err)
	}
	inner, err := r.bytes()
	if err != nil {
		return rec, fmt.Errorf("%w: %w", ErrCorruptRecord, err)
	}
	if rec.Message, err = readMessage(inner, true); err != nil {
		return rec, fmt.Errorf("%w: %w", ErrCorruptRecord, err)
	}
	if !r.done() {
		return rec, fmt.Errorf("%w: trailing bytes", ErrCorruptRecord)
	}
	return rec, nil
}

// Restore replays a journal before the first Step (ErrAlreadyStarted after). It skips
// verification (each record was verified on acceptance) and re-sends no vote; it
// returns only Notices, so a degraded rotation can say so.
func (r *Replica) Restore(records []Record) ([]Action, error) {
	if r.started {
		return nil, ErrAlreadyStarted
	}
	for i, rec := range records {
		if err := r.restoreOne(rec); err != nil {
			return nil, fmt.Errorf("record %d: %w", i, err)
		}
	}
	// Execution is redone, not replayed: lastExecuted stays at the last stable
	// checkpoint, where the application restarts, and the first Step re-emits the
	// Executes between.
	for _, e := range r.log.entries {
		if e.seq > r.lastExecuted && committedLocal(e, r.q) {
			e.committed = true
			if _, ok := r.log.committed[e.seq]; !ok {
				r.log.committed[e.seq] = e
			}
		}
	}
	// The journal is never truncated here, so it may hold long-checkpointed payloads.
	// Only payloads named by surviving log entries are kept; the rest would grow memory
	// per restart and be ordered twice.
	needed := map[Digest]bool{}
	for _, e := range r.log.entries {
		needed[e.digest] = true
	}
	for d := range r.requests {
		if !needed[d] {
			delete(r.requests, d)
		}
	}

	// A view this replica entered is a view change it finished. The records
	// arrive in the order they were written, so a NEW-VIEW or a view-entered
	// record after the VIEW-CHANGE is the answer that arrived before the
	// crash.
	if r.view >= r.pendingView {
		r.viewChanging = false
		r.pendingView = 0
	}

	// The rotation is rebuilt once, from the epoch the journal ended at,
	// rather than at every checkpoint record: a Scorer may be expensive and
	// only the last answer is ever used.
	var acts []Action
	if r.lastStable > 0 {
		if err := r.rot.recompute(r.lastStable, r.stableDigest); err != nil {
			acts = append(acts, Notice{Err: err})
		}
	}
	return acts, nil
}

func (r *Replica) restoreOne(rec Record) error {
	switch rec.Kind {
	case RecordViewEntered:
		if rec.View > r.view {
			r.view = rec.View
		}
		return nil
	case RecordCheckpointStable:
		if rec.Seq > r.lastStable {
			r.lastStable = rec.Seq
			r.stableDigest = rec.Digest
			if rec.Seq > r.lastExecuted {
				r.lastExecuted = rec.Seq
			}
			if rec.Seq > r.lastAssigned {
				r.lastAssigned = rec.Seq
			}
			// The proof is rebuilt from the CHECKPOINTs the journal already
			// replayed, which is why they are journalled at all.
			votes := r.checkpoints[checkpointKey{rec.Seq, rec.Digest}]
			r.stableProof = r.stableProof[:0]
			for _, id := range sortedIDs(votes) {
				if len(r.stableProof) == r.q {
					break
				}
				r.stableProof = append(r.stableProof, votes[id])
			}
			r.log.discardThrough(rec.Seq)
		}
		return nil
	case RecordMessage:
		return r.restoreMessage(rec.Message)
	default:
		return fmt.Errorf("%w: unknown record kind %d", ErrCorruptRecord, uint8(rec.Kind))
	}
}

func (r *Replica) restoreMessage(m Message) error {
	if err := m.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrCorruptRecord, err)
	}
	switch m.Kind {
	case KindRequest:
		r.requests[m.Digest] = m
	case KindPrePrepare, KindPrepare, KindCommit:
		if m.Seq <= r.lastStable {
			return nil
		}
		e, _, err := r.log.accept(m)
		if err != nil && err != ErrDuplicateVote {
			return fmt.Errorf("%w: a journal contradicts itself: %w", ErrCorruptRecord, err)
		}
		if m.From == r.self {
			switch m.Kind {
			case KindPrePrepare:
				// An assignment this replica made must survive a restart, or a primary hands the
				// next request a sequence it already assigned: a second signed PRE-PREPARE for one
				// (view, sequence) from a correct replica, quietly reducing the fault budget to f-1.
				if m.Seq > r.lastAssigned {
					r.lastAssigned = m.Seq
				}
				r.assigned[m.Digest] = true
			case KindPrepare:
				e.sentPrepare = true
			case KindCommit:
				e.sentCommit = true
			}
		}
		if m.View > r.view {
			r.view = m.View
		}
	case KindCheckpoint:
		r.recordCheckpoint(m)
	case KindViewChange:
		r.rememberViewChange(m)
		if m.From == r.self {
			r.sentViewChange[m.View] = true
			// A VIEW-CHANGE this replica sent is a promise not to vote below its view, which keeps
			// the next primary's O set complete. Restoring the promise is why it is journalled.
			if m.View > r.pendingView {
				r.pendingView = m.View
			}
			r.viewChanging = true
		}
	case KindNewView:
		if m.View > r.view {
			r.view = m.View
		}
		if m.From == r.self {
			r.sentNewView[m.View] = true
		}
	}
	return nil
}
