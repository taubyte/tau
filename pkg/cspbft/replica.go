package cspbft

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"time"
)

// replica.go is the state machine: four inputs, one closed set of outputs, and
// no way to reach anything else. No goroutine, no socket, no clock read, no
// disk write.

// Timer is one of the two local timers PBFT needs. Both are logical: the core
// says how long, the caller says when it elapsed, and no wall clock is ever
// compared across replicas.
type Timer uint8

// The two timers.
const (
	// TimerRequest is a backup waiting for a request it knows about to be
	// executed. Its expiry is how a silent or censoring primary is noticed.
	TimerRequest Timer = iota + 1

	// TimerNewView is a replica waiting for the NEW-VIEW that should follow
	// its VIEW-CHANGE. Its expiry doubles the timeout and moves on, which is
	// what keeps a run of faulty primaries from stalling the protocol forever.
	TimerNewView
)

func (w Timer) String() string {
	switch w {
	case TimerRequest:
		return "request"
	case TimerNewView:
		return "new-view"
	default:
		return fmt.Sprintf("Timer(%d)", uint8(w))
	}
}

// Action is something the caller must do. The set is closed — ten types, and
// adding an eleventh is a deliberate review against the protocol's invariants
// rather than a convenience.
type Action interface{ isAction() }

// Send delivers one message to one replica.
type Send struct {
	To      ReplicaID
	Message Message
}

// Broadcast delivers one message to every replica except this one.
type Broadcast struct{ Message Message }

// Persist must be made DURABLE BEFORE ANY LATER ACTION IN THE SAME SLICE IS
// PERFORMED. That ordering is the whole durability contract: a vote that was
// sent but not remembered is how a crashed replica contradicts itself.
type Persist struct{ Records []Record }

// Execute is the ordering decision, which is all this package produces. It
// carries everything a reply needs; the caller signs and sends that itself,
// because this package never sees an application result.
//
// EXACTLY-ONCE IS THE APPLICATION'S, AND DELIBERATELY SO. A view change can
// legitimately order one request twice — the new primary re-proposes what it
// cannot prove was executed — so Execute carries Client and Serial and the
// application's reply table suppresses the repeat. This package keeps no such
// table, because its table would not be part of the state a checkpoint
// digests, and after a state transfer it would be empty on one replica and
// full on the others. Two replicas suppressing different executions is
// divergence, which is worse than an application doing the same check twice.
type Execute struct {
	Seq      Sequence
	View     View
	Client   ClientID
	Serial   uint64
	Proposal Proposal
}

// TakeCheckpoint asks the caller to digest its application state at Seq.
// Answer with Checkpointed.
type TakeCheckpoint struct{ Seq Sequence }

// FetchState says this replica is behind: the state at Seq with digest Digest
// is certified by the replicas in From. Moving the bytes is the caller's, since
// it is bulk I/O and this package has none. Answer with Installed.
type FetchState struct {
	Seq    Sequence
	Digest Digest
	From   []ReplicaID
}

// SetTimer starts or restarts a timer. Answer its expiry with Timeout.
type SetTimer struct {
	Which Timer
	After time.Duration
}

// CancelTimer stops a timer.
type CancelTimer struct{ Which Timer }

// Evidence hands out transferable proof that a replica equivocated. The caller
// decides what to do with it; see evidence.go for why this package does not.
type Evidence struct{ Proof Equivocation }

// Notice is non-fatal: a degraded Scorer, a refused message, something worth a
// log line and no reaction.
type Notice struct{ Err error }

func (Send) isAction()           {}
func (Broadcast) isAction()      {}
func (Persist) isAction()        {}
func (Execute) isAction()        {}
func (TakeCheckpoint) isAction() {}
func (FetchState) isAction()     {}
func (SetTimer) isAction()       {}
func (CancelTimer) isAction()    {}
func (Evidence) isAction()       {}
func (Notice) isAction()         {}

// Replica is one participant's whole state. It is not safe for concurrent use:
// the caller owns the loop, and a state machine that locked itself would be
// making a concurrency decision on the caller's behalf.
type Replica struct {
	opts     *options
	replicas []ReplicaID
	self     ReplicaID
	n, f, q  int
	k        uint64

	rot *rotation
	log *messageLog

	view         View
	pendingView  View
	viewChanging bool
	started      bool

	// broken is set when the Signer fails. A replica that cannot sign cannot
	// vote, and pretending otherwise would leave the log holding a vote that
	// was never sent — so every entry point refuses from then on.
	broken error

	requests    map[Digest]Message
	orphans     map[Digest][]Message
	orphansHeld int
	arrival     []Digest
	outstanding map[Digest]bool
	assigned    map[Digest]bool

	// executedAt is what this replica has executed ABOVE THE LAST STABLE
	// CHECKPOINT, and it is what stops a client's retransmission being ordered
	// a second time inside one checkpoint interval.
	//
	// ITS LIFETIME IS AGREED RATHER THAN LOCAL, and that is the whole point. A
	// replica that took the state at a checkpoint by transfer never executed
	// anything below it, so any memory reaching further back would be held by
	// the replicas that were there and not by the one that was not — and then
	// a retransmission is dropped by a primary that remembers and held
	// outstanding by a backup that does not, which is a request that can never
	// be ordered and a backup that forces a view change every timeout over it.
	// Cut at the checkpoint, every replica forgets the same digests at the
	// same point, and a retransmission that arrives after it costs one
	// sequence number on every replica alike.
	executedAt map[Digest]Sequence

	lastStable   Sequence
	stableDigest Digest
	stableProof  []Message
	lastExecuted Sequence
	lastAssigned Sequence

	checkpoints map[checkpointKey]map[ReplicaID]Message

	viewChanges    map[View]map[ReplicaID]Message
	sentViewChange map[View]bool
	sentNewView    map[View]bool

	timeout      time.Duration
	timerRunning map[Timer]bool
}

// CurrentView is the view this replica believes it is in.
func (r *Replica) CurrentView() View { return r.view }

// LastExecuted is the highest sequence number this replica has executed.
func (r *Replica) LastExecuted() Sequence { return r.lastExecuted }

// Primary is who leads view v under the current rotation. It is perm[v mod n],
// and between stable checkpoints perm does not move.
func (r *Replica) Primary(v View) ReplicaID { return r.rot.primary(v) }

// Epoch is the stable checkpoint the current rotation is bound to.
func (r *Replica) Epoch() Sequence { return r.rot.epoch }

// Watermarks is (h, h+L]: the window of sequence numbers this replica will
// accept. It is what stops a faulty primary exhausting the sequence space.
func (r *Replica) Watermarks() (low, high Sequence) {
	return r.lastStable, r.lastStable + Sequence(2*r.k)
}

func (r *Replica) isPrimary() bool { return r.rot.primary(r.view) == r.self }

func (r *Replica) known(id ReplicaID) bool { return contains(r.replicas, id) }

// sign is the one place a Signer is called. Its failure is recorded rather
// than returned from a dozen helpers, and then refused at the entry point,
// because by the time a vote cannot be signed the replica is already unable to
// take part.
func (r *Replica) sign(m Message) Message {
	sig, err := r.opts.sign(Preimage(m))
	if err != nil {
		r.broken = fmt.Errorf("cspbft: signer failed, this replica can no longer vote: %w", err)
		return m
	}
	m.Signature = sig
	return m
}

func rec(m Message) Record { return Record{Kind: RecordMessage, Message: m} }

// Step is the one input every protocol message goes through.
//
// VERIFICATION HAPPENS HERE, BEFORE THE MESSAGE TOUCHES ANY LOG. A message
// that has not been verified must never be able to influence non-equivocation
// state — otherwise an unsigned forgery naming replica j can make this replica
// refuse j's real messages as equivocation, which turns one forged packet into
// a permanent accusation.
func (r *Replica) Step(m Message) ([]Action, error) {
	r.started = true
	if r.broken != nil {
		return nil, r.broken
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	if m.Kind == KindRequest {
		if err := r.opts.verifyCl(m.Client, Preimage(m), m.Signature); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrBadSignature, err)
		}
	} else {
		if !r.known(m.From) {
			return nil, fmt.Errorf("%w: %q", ErrNotInConfiguration, m.From)
		}
		if err := r.opts.verify(m.From, Preimage(m), m.Signature); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrBadSignature, err)
		}
	}

	acts, err := r.dispatch(m)
	if r.broken != nil {
		return nil, r.broken
	}
	return acts, err
}

func (r *Replica) dispatch(m Message) ([]Action, error) {
	switch m.Kind {
	case KindRequest:
		return r.onRequest(m)
	case KindPrePrepare:
		return r.onPrePrepare(m)
	case KindPrepare:
		return r.onPrepare(m)
	case KindCommit:
		return r.onCommit(m)
	case KindCheckpoint:
		return r.onCheckpoint(m)
	case KindViewChange:
		return r.onViewChange(m)
	case KindNewView:
		return r.onNewView(m)
	default:
		return nil, fmt.Errorf("%w: unknown kind %d", ErrMalformed, uint8(m.Kind))
	}
}

// Timeout is the caller saying a timer it was asked to set has elapsed. It is
// the ONLY way time enters this package.
func (r *Replica) Timeout(w Timer) []Action {
	r.started = true
	if r.broken != nil {
		return []Action{Notice{Err: r.broken}}
	}
	if w != TimerRequest && w != TimerNewView {
		return []Action{Notice{Err: fmt.Errorf("%w: %d", ErrUnknownTimer, uint8(w))}}
	}
	r.timerRunning[w] = false

	target := r.view + 1
	if r.viewChanging {
		// A view change that itself timed out means the next primary is also
		// unreachable, so the wait doubles. Without the doubling a partition
		// burns through views faster than a NEW-VIEW can cross it.
		r.timeout *= 2
		if r.timeout > r.opts.max {
			r.timeout = r.opts.max
		}
		if r.pendingView >= target {
			target = r.pendingView + 1
		}
	}
	// A VIEW THIS REPLICA HAS ALREADY ASKED FOR IS NOT ASKED FOR AGAIN, AND
	// THE NEXT ONE IS ASKED FOR INSTEAD. The timer is already marked stopped
	// by the time this runs, and nothing but an inbound assignment re-arms it,
	// so a replica that escalated past a view and then entered that view
	// through a NEW-VIEW would find startViewChange refusing and be left
	// holding an unexecuted request with no fault detector at all.
	for r.sentViewChange[target] {
		target++
	}
	acts := r.startViewChange(target)
	if r.broken != nil {
		return []Action{Notice{Err: r.broken}}
	}
	return acts
}

// onRequest: a client's proposal, arriving either straight from the client or
// forwarded by whoever saw it first.
func (r *Replica) onRequest(m Message) ([]Action, error) {
	if m.Digest != RequestDigest(r.opts.hash, m.Client, m.Serial, m.Proposal) {
		return nil, fmt.Errorf("%w: a request's digest is not the digest of its content", ErrMalformed)
	}
	var acts []Action
	if _, have := r.requests[m.Digest]; !have {
		// THE PAYLOAD IS PART OF THE JOURNAL. A restarted replica that holds a
		// committed digest and not the bytes behind it cannot execute, and
		// execution is strictly in order, so it cannot execute anything after
		// it either.
		r.requests[m.Digest] = m
		acts = append(acts, Persist{Records: []Record{rec(m)}})
	}

	// HOLDING THE BYTES IS NOT THE SAME QUESTION AS STILL WAITING FOR THEM,
	// and reading the one as the other loses requests in both directions. A
	// restored journal hands back payloads with nothing outstanding behind
	// them, and a new primary legitimately re-proposes at a second sequence a
	// request this replica already holds — so what decides is whether this
	// replica is waiting for it, has assigned it, or has executed it.
	_, done := r.executedAt[m.Digest]
	known := done || r.outstanding[m.Digest] || r.assigned[m.Digest]
	if !known {
		r.arrival = append(r.arrival, m.Digest)
		r.outstanding[m.Digest] = true
	}

	acts = append(acts, r.drainOrphans(m.Digest)...)
	if r.isPrimary() && !r.viewChanging {
		acts = append(acts, r.assignPending()...)
	} else if !known {
		// A BACKUP FORWARDS TO THE PRIMARY AND STARTS ITS TIMER. The forward
		// is how a client that can only reach one replica still gets ordered;
		// the timer is how a primary that drops it gets replaced.
		acts = append(acts, Send{To: r.rot.primary(r.view), Message: m})
	}
	acts = append(acts, r.execute()...)
	return acts, nil
}

// assignPending gives sequence numbers to everything the primary holds and has
// not yet assigned, in arrival order, while the water marks allow.
func (r *Replica) assignPending() []Action {
	if !r.isPrimary() || r.viewChanging {
		return nil
	}
	_, high := r.Watermarks()
	var acts []Action
	for _, d := range r.arrival {
		if r.assigned[d] || !r.outstanding[d] {
			continue
		}
		next := r.lastAssigned + 1
		if next > high {
			break
		}
		r.lastAssigned = next
		r.assigned[d] = true
		pp := r.sign(Message{Kind: KindPrePrepare, From: r.self, View: r.view, Seq: next, Digest: d})
		if r.broken != nil {
			return nil
		}
		// THE LOG DECIDES BEFORE THE VOTE LEAVES, not after. A contradiction
		// found once the Broadcast is already in the slice is a second signed
		// assignment of one sequence number that the caller has been told to
		// send — which is the fault the whole protocol is built to make
		// impossible, produced by a replica that is otherwise correct.
		e, eq, err := r.log.accept(pp)
		if eq != nil {
			acts = append(acts, Evidence{Proof: *eq}, Notice{Err: err})
			return acts
		}
		if err != nil {
			acts = append(acts, Notice{Err: err})
			continue
		}
		acts = append(acts, Persist{Records: []Record{rec(pp)}})
		acts = append(acts, Broadcast{Message: r.requests[d]}, Broadcast{Message: pp})
		acts = append(acts, r.afterAccept(e)...)
	}
	return acts
}

func (r *Replica) onPrePrepare(m Message) ([]Action, error) {
	if err := r.viewOf(m); err != nil {
		return nil, err
	}
	if r.viewChanging {
		return nil, ErrStaleView
	}
	if m.From != r.rot.primary(m.View) {
		return nil, fmt.Errorf("%w: %q is not the primary of view %d", ErrNotPrimary, m.From, m.View)
	}
	if err := r.inWatermarks(m.Seq); err != nil {
		return nil, err
	}
	if e := r.log.at(m.View, m.Seq); e != nil && e.prePrepare != nil && e.prePrepare.Digest != m.Digest {
		// A SECOND ASSIGNMENT IS A CONTRADICTION WHETHER OR NOT THE PAYLOAD
		// BEHIND IT IS HERE. Held below until the bytes arrive, the proof
		// would depend on the network delivering a request this replica has
		// no other use for — and the primary that equivocated is the one
		// deciding whether it ever does.
		return []Action{Evidence{Proof: Equivocation{A: *e.prePrepare, B: m}}}, ErrEquivocation
	}
	if _, ok := r.requests[m.Digest]; !ok && !m.Digest.IsNull() {
		// THE PAYLOAD HAS NOT ARRIVED YET, and that is ordinary reordering
		// rather than a fault. Holding the assignment until it does is what
		// keeps a correct primary from being punished for the network's
		// choices; the orphan set is bounded by the water marks.
		r.hold(m)
		return nil, nil
	}
	return r.acceptPrePrepare(m)
}

func (r *Replica) acceptPrePrepare(m Message) ([]Action, error) {
	e, eq, err := r.log.accept(m)
	if eq != nil {
		return []Action{Evidence{Proof: *eq}}, ErrEquivocation
	}
	if err != nil {
		return nil, err
	}
	// THE ASSIGNMENT IS JOURNALLED, NOT ONLY THE VOTE ON IT. A prepared
	// certificate is a pre-prepare plus the prepares that match it, and a
	// replica that restarted holding only its own vote could not produce the
	// certificate its next VIEW-CHANGE has to carry — which is the same thing
	// as agreeing to forget an assignment a quorum already made.
	records := []Record{rec(m)}
	var acts []Action
	var out []Message
	if !e.sentPrepare && m.From != r.self {
		e.sentPrepare = true
		p := r.sign(Message{Kind: KindPrepare, From: r.self, View: m.View, Seq: m.Seq, Digest: m.Digest})
		if r.broken != nil {
			return nil, r.broken
		}
		records = append(records, rec(p))
		out = append(out, p)
		if _, _, err := r.log.accept(p); err != nil {
			acts = append(acts, Notice{Err: err})
		}
	}
	acts = append([]Action{Persist{Records: records}}, acts...)
	for _, p := range out {
		acts = append(acts, Broadcast{Message: p})
	}
	acts = append(acts, r.afterAccept(e)...)
	acts = append(acts, r.armRequestTimer()...)
	return acts, nil
}

// hold keeps an assignment until its payload arrives.
//
// THE BOUND IS ON MESSAGES AND NOT ON DIGESTS. One digest can be assigned at
// every sequence in the window, and a faulty primary can retransmit any of
// them forever, so counting distinct digests bounds nothing an adversary
// controls — the slice under one key grows instead. What is kept is at most
// one assignment per (view, sequence) per digest, and at most 4K in total:
// twice the window, which leaves exactly the room a second digest at one
// sequence needs, because that pair is the proof the primary equivocated.
func (r *Replica) hold(m Message) {
	for _, h := range r.orphans[m.Digest] {
		if h.View == m.View && h.Seq == m.Seq {
			return
		}
	}
	if r.orphansHeld >= 4*int(r.k) {
		return
	}
	r.orphans[m.Digest] = append(r.orphans[m.Digest], m)
	r.orphansHeld++
}

// dropOrphans forgets everything held for one digest, and is the only way the
// held count goes down: a delete that forgot to adjust it would leak the bound
// rather than the memory, which is worse because nothing would show.
func (r *Replica) dropOrphans(d Digest) []Message {
	held := r.orphans[d]
	r.orphansHeld -= len(held)
	delete(r.orphans, d)
	return held
}

func (r *Replica) drainOrphans(d Digest) []Action {
	held := r.dropOrphans(d)
	if len(held) == 0 {
		return nil
	}
	var acts []Action
	for _, pp := range held {
		if pp.View != r.view || r.viewChanging {
			continue
		}
		if r.inWatermarks(pp.Seq) != nil {
			continue
		}
		a, err := r.acceptPrePrepare(pp)
		acts = append(acts, a...)
		if err != nil {
			acts = append(acts, Notice{Err: err})
		}
	}
	return acts
}

func (r *Replica) onPrepare(m Message) ([]Action, error) {
	if err := r.viewOf(m); err != nil {
		return nil, err
	}
	if err := r.inWatermarks(m.Seq); err != nil {
		return nil, err
	}
	e, eq, err := r.log.accept(m)
	if eq != nil {
		return []Action{Evidence{Proof: *eq}}, ErrEquivocation
	}
	if err != nil {
		return nil, err
	}
	acts := []Action{Persist{Records: []Record{rec(m)}}}
	return append(acts, r.afterAccept(e)...), nil
}

func (r *Replica) onCommit(m Message) ([]Action, error) {
	if err := r.viewOf(m); err != nil {
		return nil, err
	}
	if err := r.inWatermarks(m.Seq); err != nil {
		return nil, err
	}
	e, eq, err := r.log.accept(m)
	if eq != nil {
		return []Action{Evidence{Proof: *eq}}, ErrEquivocation
	}
	if err != nil {
		return nil, err
	}
	acts := []Action{Persist{Records: []Record{rec(m)}}}
	return append(acts, r.afterAccept(e)...), nil
}

// afterAccept is the only place a phase advances: it asks whether the vote
// just recorded completed a certificate, and acts once if it did.
func (r *Replica) afterAccept(e *entry) []Action {
	var acts []Action
	if !e.sentCommit && prepared(e, r.q) {
		e.sentCommit = true
		c := r.sign(Message{Kind: KindCommit, From: r.self, View: e.view, Seq: e.seq, Digest: e.digest})
		if r.broken != nil {
			return nil
		}
		acts = append(acts, Persist{Records: []Record{rec(c)}}, Broadcast{Message: c})
		if _, _, err := r.log.accept(c); err != nil {
			acts = append(acts, Notice{Err: err})
		}
	}
	if !e.committed && committedLocal(e, r.q) {
		e.committed = true
		if _, ok := r.log.committed[e.seq]; !ok {
			r.log.committed[e.seq] = e
		}
		acts = append(acts, r.execute()...)
	}
	return acts
}

// execute runs the order, strictly in sequence. A gap stops it: a hole in the
// order is not something to work around, it is something to wait for.
func (r *Replica) execute() []Action {
	var acts []Action
	progressed := false
	for {
		next := r.lastExecuted + 1
		e, ok := r.log.committed[next]
		if !ok {
			break
		}
		if e.digest.IsNull() {
			// A null request fills a gap a view change could not resolve. It
			// consumes the sequence number and does nothing.
			r.lastExecuted = next
			progressed = true
		} else {
			req, ok := r.requests[e.digest]
			if !ok {
				// Committed, but the payload never arrived. Nothing after this
				// may execute: order is the product.
				break
			}
			r.lastExecuted = next
			progressed = true
			r.executedAt[e.digest] = next
			delete(r.outstanding, e.digest)
			acts = append(acts, Execute{
				Seq: next, View: e.view, Client: req.Client, Serial: req.Serial, Proposal: req.Proposal,
			})
		}
		if uint64(next)%r.k == 0 {
			acts = append(acts, TakeCheckpoint{Seq: next})
		}
	}
	if progressed {
		// LV2: EXECUTING IN A VIEW MEANS THAT VIEW WORKS, so the doubled
		// timeout goes back to the base. Without the reset one bad stretch
		// leaves the cluster slow to notice the next fault, forever.
		r.timeout = r.opts.base
	}
	acts = append(acts, r.reviewRequestTimer()...)
	return acts
}

// armRequestTimer starts the request timer if anything is outstanding.
func (r *Replica) armRequestTimer() []Action {
	if r.isPrimary() || len(r.outstanding) == 0 || r.timerRunning[TimerRequest] {
		return nil
	}
	r.timerRunning[TimerRequest] = true
	return []Action{SetTimer{Which: TimerRequest, After: r.timeout}}
}

func (r *Replica) reviewRequestTimer() []Action {
	if len(r.outstanding) == 0 {
		if r.timerRunning[TimerRequest] {
			r.timerRunning[TimerRequest] = false
			return []Action{CancelTimer{Which: TimerRequest}}
		}
		return nil
	}
	return r.armRequestTimer()
}

func (r *Replica) viewOf(m Message) error {
	switch {
	case m.View < r.view:
		return fmt.Errorf("%w: view %d, this replica is in %d", ErrStaleView, m.View, r.view)
	case m.View > r.view:
		return fmt.Errorf("%w: view %d, this replica is in %d", ErrFutureView, m.View, r.view)
	}
	return nil
}

func (r *Replica) inWatermarks(n Sequence) error {
	low, high := r.Watermarks()
	if n <= low || n > high {
		return fmt.Errorf("%w: sequence %d is not in (%d, %d]", ErrOutOfWatermarks, n, low, high)
	}
	return nil
}

// RequestDigest is what replicas agree about when they order a request: the
// client, its counter and the payload, length-delimited so no two distinct
// requests can produce one digest.
//
// A CLIENT COMPUTES THIS ITSELF and puts it in Message.Digest; Step refuses a
// request whose digest is not the digest of its content, because a request
// that says it is something else would be ordered under one name and executed
// under another.
func RequestDigest(h Hasher, client ClientID, serial uint64, p Proposal) Digest {
	b := make([]byte, 0, 32+len(client)+len(p))
	b = append(b, "cspbft/request/v1\x00"...)
	b = appendBytes(b, []byte(client))
	b = binary.BigEndian.AppendUint64(b, serial)
	b = appendBytes(b, p)
	return h(b)
}

// SHA256 is the default Hasher, exported so a client can compute a
// RequestDigest without constructing a Replica.
func SHA256(b []byte) Digest { return sha256.Sum256(b) }
