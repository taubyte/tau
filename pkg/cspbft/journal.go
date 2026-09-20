package cspbft

import (
	"encoding/binary"
	"fmt"
)

// journal.go names what must survive a crash, and replays it.
//
// THIS PACKAGE PERSISTS NOTHING. It emits Persist actions, and the contract is
// one sentence: a Persist must be durable before any action that follows it in
// the same slice is performed. Step always places the Persist that authorises
// a vote before the Broadcast of that vote.
//
// The reason is safety and not tidiness. A replica that prepared (v,n,d),
// crashed, and came back having forgotten it will happily prepare (v,n,d') —
// and two prepared certificates for one (view, sequence) is exactly the thing
// the protocol is built to make impossible.
//
// A record is, with two exceptions, THE SIGNED MESSAGE ITSELF, so there is no
// second serialization to keep in step with the one signatures are taken over:
// the journal uses preimage.go's layout with the signature included. The two
// local facts that are not messages are the view this replica entered and the
// checkpoint it made stable, and both are facts about this replica alone.

// RecordKind is which of the three things a Record holds.
type RecordKind uint8

// The three record kinds.
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

// Record is one durable fact.
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

// Restore replays a journal. It must be called before the first Step:
// ErrAlreadyStarted after.
//
// VERIFICATION IS SKIPPED AND NO VOTE IS RE-SENT. Every record here was
// verified when it was first accepted, and re-broadcasting a vote the replica
// already sent would be a retransmission storm on every restart. What Restore
// rebuilds is exactly the memory that makes the replica refuse to contradict
// itself. The actions it does return are Notices and nothing else: a replica
// that comes back with a degraded rotation has to be able to say so, which is
// clause 6 of the Scorer contract and the one seam a restart can break.
func (r *Replica) Restore(records []Record) ([]Action, error) {
	if r.started {
		return nil, ErrAlreadyStarted
	}
	for i, rec := range records {
		if err := r.restoreOne(rec); err != nil {
			return nil, fmt.Errorf("record %d: %w", i, err)
		}
	}
	// EXECUTION IS NOT REPLAYED, it is redone. Restore leaves lastExecuted at
	// the last stable checkpoint — which is where the caller's application
	// also restarts — and rebuilds which sequences are committed, so the first
	// Step after a restart re-emits the Execute actions between the two.
	for _, e := range r.log.entries {
		if e.seq > r.lastExecuted && committedLocal(e, r.q) {
			e.committed = true
			if _, ok := r.log.committed[e.seq]; !ok {
				r.log.committed[e.seq] = e
			}
		}
	}
	// A JOURNAL IS NOT TRUNCATED BY THIS PACKAGE, so it can hold every payload
	// this replica ever accepted — including requests executed and
	// checkpointed long ago, whose bytes are already inside the state the
	// application restarts from. What is needed is exactly the payloads the
	// surviving log entries name. Keeping the rest would grow a replica's
	// memory with every restart it ever performs, and treating them as still
	// pending would order each of them a second time.
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
				// AN ASSIGNMENT THIS REPLICA MADE IS THE ONE FACT A RESTART
				// MUST NOT LOSE. Without it a primary comes back believing it
				// has assigned nothing since its last stable checkpoint, and
				// hands the next request a sequence number it has already
				// ordered — a second PRE-PREPARE for one (view, sequence)
				// under its own signature. That is the fault the whole
				// protocol is built to make impossible, and it makes a
				// correct replica produce it, so the fault budget quietly
				// becomes f-1 and two correct replicas can execute different
				// proposals at one sequence number.
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
			// A VIEW-CHANGE THIS REPLICA SENT IS A PROMISE NOT TO VOTE BELOW
			// THE VIEW IT NAMES, and the promise is what makes the next
			// primary's O set complete. A certificate prepared after the
			// VIEW-CHANGE went out appears in no V that NEW-VIEW is built
			// from, so a replica that came back from a crash willing to vote
			// in the old view again would let one quorum commit a digest the
			// next view re-proposes differently — with nothing byzantine
			// anywhere. Putting the promise back is the reason the message is
			// journalled at all.
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
