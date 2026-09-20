package cspbft

import (
	"fmt"
	"sort"
)

// viewchange.go is how a faulty primary is replaced without losing anything a
// correct replica has already been promised.
//
// THE ONE oSet FUNCTION IS THE POINT OF THIS FILE. The new primary builds the
// O set with it, and every backup INDEPENDENTLY RECOMPUTES the same O from the
// same V with the same function and refuses a NEW-VIEW that does not match. If
// the two sides were written twice they could differ, and the difference would
// be a safety failure rather than a mismatch — a backup accepting an O that
// quietly dropped a prepared certificate is a backup agreeing to forget an
// assignment a quorum already made.

// startViewChange moves toward target.
//
// AT MOST ONE VIEW-CHANGE PER VIEW, ever. Two would let one replica supply two
// of the votes a NEW-VIEW needs, which is the quorum arithmetic breaking in
// the least visible way possible.
func (r *Replica) startViewChange(target View) []Action {
	if target <= r.view || r.sentViewChange[target] {
		return nil
	}
	if !r.roomForViewChange(target, r.self) {
		// ASKING FOR A VIEW FURTHER OUT THAN THIS BUYS NOTHING. A run of
		// faulty primaries is at most 2f views long, so one of the 2f+2 views
		// this replica is already asking for has a correct primary: the answer
		// is inside that window or it is not coming. The budget can only be
		// full because this replica filled it, so it is already waiting — and
		// what it must not do is stop, because Timeout has already marked the
		// timer stopped and nothing else re-arms it.
		return r.restartNewViewTimer()
	}
	r.viewChanging = true
	if target > r.pendingView {
		r.pendingView = target
	}
	vc := r.sign(Message{
		Kind:        KindViewChange,
		From:        r.self,
		View:        target,
		Seq:         r.lastStable,
		Digest:      r.stableDigest,
		Checkpoints: r.stableProof,
		Prepared:    r.log.preparedCertificates(r.lastStable, r.q),
	})
	if r.broken != nil {
		return nil
	}
	r.sentViewChange[target] = true
	acts := []Action{Persist{Records: []Record{rec(vc)}}, Broadcast{Message: vc}}
	r.rememberViewChange(vc)

	if r.timerRunning[TimerRequest] {
		r.timerRunning[TimerRequest] = false
		acts = append(acts, CancelTimer{Which: TimerRequest})
	}
	acts = append(acts, r.restartNewViewTimer()...)

	acts = append(acts, r.maybeSendNewView(target)...)
	return acts
}

func (r *Replica) restartNewViewTimer() []Action {
	r.timerRunning[TimerNewView] = true
	return []Action{SetTimer{Which: TimerNewView, After: r.timeout}}
}

// roomForViewChange is the water mark a VIEW-CHANGE would otherwise not have.
//
// It is the only message in the protocol whose position is a view rather than
// a sequence number, so nothing else bounds how far ahead one may name — and
// every one that is accepted costs a map entry and a JOURNAL RECORD on every
// correct replica, which is unbounded disk at one cheap packet each.
//
// THE BOUND IS PER SENDER, so no replica can crowd out another's, and what
// fits is the first 2f+2 views a sender asks for above this one. 2f is the
// longest run of faulty primaries the scoring layer allows, so a correct
// primary is always inside that window: a view past it is not an answer this
// replica is waiting for.
func (r *Replica) roomForViewChange(v View, from ReplicaID) bool {
	if _, ok := r.viewChanges[v][from]; ok {
		return true
	}
	held := 0
	for at, byID := range r.viewChanges {
		if at <= r.view {
			continue
		}
		if _, ok := byID[from]; ok {
			held++
		}
	}
	return held < 2*r.f+2
}

func (r *Replica) rememberViewChange(m Message) {
	if r.viewChanges[m.View] == nil {
		r.viewChanges[m.View] = map[ReplicaID]Message{}
	}
	r.viewChanges[m.View][m.From] = m
}

func (r *Replica) onViewChange(m Message) ([]Action, error) {
	if m.View <= r.view {
		return nil, fmt.Errorf("%w: a view-change to %d, this replica is in %d", ErrStaleView, m.View, r.view)
	}
	// The cheap refusal comes before the expensive check on purpose:
	// validateViewChange verifies up to L certificates of q-1 signatures each,
	// which is the most expensive thing a sender can ask this replica to do.
	if !r.roomForViewChange(m.View, m.From) {
		return nil, fmt.Errorf("%w: %q is already asking for %d views above %d", ErrTooManyViewChanges, m.From, 2*r.f+2, r.view)
	}
	if err := validateViewChange(m, r.q, r.opts.verify, r.known); err != nil {
		return nil, err
	}
	r.rememberViewChange(m)
	acts := []Action{Persist{Records: []Record{rec(m)}}}
	acts = append(acts, r.hearsay()...)
	acts = append(acts, r.maybeSendNewView(m.View)...)
	return acts, nil
}

// hearsay is Castro-Liskov's f+1 rule: f+1 replicas asking for a higher view
// includes at least one correct replica, so this one is behind and moves
// without waiting for its own timer.
//
// WITHOUT IT A LAGGING REPLICA IS STRANDED. The quorum that already moved will
// not talk to it about the old view, and its own timer may be long.
func (r *Replica) hearsay() []Action {
	senders := map[ReplicaID]bool{}
	smallest := View(0)
	for v, byID := range r.viewChanges {
		if v <= r.view {
			continue
		}
		// THE RULE IS f+1 OTHERS, AND THE SMALLEST VIEW IN THEIR SET. Counting
		// this replica's own asks weakens f+1 to f, which f faulty replicas
		// reach by themselves; and letting a view only this replica asked for
		// be the smallest pins it to a view nobody else is joining, so the
		// claim it is supposed to be acting on is never acted on at all.
		others := 0
		for id := range byID {
			if id == r.self {
				continue
			}
			senders[id] = true
			others++
		}
		if others == 0 {
			continue
		}
		if smallest == 0 || v < smallest {
			smallest = v
		}
	}
	if smallest == 0 || len(senders) < r.f+1 || r.sentViewChange[smallest] {
		return nil
	}
	return r.startViewChange(smallest)
}

// maybeSendNewView builds the NEW-VIEW once the primary of the target view
// holds a quorum of VIEW-CHANGEs for it.
func (r *Replica) maybeSendNewView(v View) []Action {
	if v <= r.view || r.sentNewView[v] || r.rot.primary(v) != r.self {
		return nil
	}
	byID := r.viewChanges[v]
	if len(byID) < r.q {
		return nil
	}
	v0 := r.selectViewChanges(byID)

	o, minS, maxS, err := r.oSet(v0, v)
	if err != nil {
		return []Action{Notice{Err: err}}
	}
	signed := make([]Message, 0, len(o))
	for _, pp := range o {
		signed = append(signed, r.sign(pp))
		if r.broken != nil {
			return nil
		}
	}
	nv := r.sign(Message{
		Kind:           KindNewView,
		From:           r.self,
		View:           v,
		ViewChanges:    v0,
		PrePrepares:    signed,
		Epoch:          r.rot.epoch,
		RotationDigest: r.rot.digest,
	})
	if r.broken != nil {
		return nil
	}
	r.sentNewView[v] = true
	acts := []Action{Persist{Records: []Record{rec(nv)}}, Broadcast{Message: nv}}
	acts = append(acts, r.installNewView(v, v0, signed, minS, maxS)...)
	return acts
}

// selectViewChanges picks exactly q, this replica's own first when it has one.
// It is deterministic because the result is signed: map order in a signed
// message is a message that differs between two runs of the same code.
func (r *Replica) selectViewChanges(byID map[ReplicaID]Message) []Message {
	out := make([]Message, 0, r.q)
	if own, ok := byID[r.self]; ok {
		out = append(out, own)
	}
	for _, id := range sortedIDs(byID) {
		if len(out) == r.q {
			break
		}
		if id == r.self {
			continue
		}
		out = append(out, byID[id])
	}
	return out
}

// oSet is the O set: what the new view re-proposes.
//
// min-s is the highest stable checkpoint anyone in V has, max-s the highest
// sequence anyone prepared. For every sequence between them, the digest from
// the HIGHEST-VIEW prepared certificate wins — that is the cross-view
// stickiness the whole protocol turns on — and a sequence nobody prepared is
// filled with a null request, because leaving a hole would stop execution
// forever.
func (r *Replica) oSet(v0 []Message, newView View) ([]Message, Sequence, Sequence, error) {
	var minS, maxS Sequence
	best := map[Sequence]Message{}
	for _, vc := range v0 {
		if vc.Seq > minS {
			minS = vc.Seq
		}
		for _, c := range vc.Prepared {
			pp := c.PrePrepare
			if pp.Seq > maxS {
				maxS = pp.Seq
			}
			cur, ok := best[pp.Seq]
			if !ok || pp.View > cur.View || (pp.View == cur.View && bytesLess(cur.Digest, pp.Digest)) {
				best[pp.Seq] = pp
			}
		}
	}
	if maxS < minS {
		maxS = minS
	}
	if maxS > minS+Sequence(2*r.k) {
		// A certificate above the water marks cannot have been accepted by a
		// correct replica, so V is not what it claims to be.
		return nil, 0, 0, fmt.Errorf("%w: a view-change claims sequence %d above a checkpoint at %d", ErrBadCertificate, maxS, minS)
	}
	out := make([]Message, 0, maxS-minS)
	for n := minS + 1; n <= maxS; n++ {
		d := nullDigest
		if pp, ok := best[n]; ok {
			d = pp.Digest
		}
		out = append(out, Message{Kind: KindPrePrepare, From: r.rot.primary(newView), View: newView, Seq: n, Digest: d})
	}
	return out, minS, maxS, nil
}

// bytesLess breaks a tie between two certificates from the same view.
//
// IT CANNOT FIRE WHILE THE FAULT BUDGET HOLDS, and the argument is worth
// writing down because it is the budget and not the arithmetic that makes it
// true. Two prepared certificates for one (view, sequence) need an
// equivocating primary AND a backup willing to prepare both digests, which is
// two faulty replicas; with one, the correct backups each prepare once and
// there are not enough votes to go round. The budget is intact only because a
// correct replica never signs two assignments for one (view, sequence) — which
// is what restoring lastAssigned from the journal is for, and why an amnesiac
// primary is a safety failure and not a lost message. "Impossible" is still no
// reason for two replicas to compute different O sets, so the tie-break is
// explicit.
func bytesLess(a, b Digest) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

func (r *Replica) onNewView(m Message) ([]Action, error) {
	if m.View < r.view {
		return nil, fmt.Errorf("%w: a new-view for %d, this replica is in %d", ErrStaleView, m.View, r.view)
	}
	if m.View == r.view {
		return nil, fmt.Errorf("%w: a new-view for %d, which this replica already installed", ErrDuplicateVote, m.View)
	}
	// THE ROTATION IS CHECKED BEFORE THE PRIMARY IS, because if the two sides
	// disagree about the permutation then "is the sender the primary" is not a
	// question with a shared answer. This is the one failure the Scorer seam
	// can cause, and it gets a name instead of a stall.
	if m.Epoch != r.rot.epoch || m.RotationDigest != r.rot.digest {
		return nil, fmt.Errorf("%w: new-view names epoch %d, this replica is at %d", ErrRotationMismatch, m.Epoch, r.rot.epoch)
	}
	if m.From != r.rot.primary(m.View) {
		return nil, fmt.Errorf("%w: %q is not the primary of view %d", ErrNotPrimary, m.From, m.View)
	}
	if len(m.ViewChanges) < r.q {
		return nil, fmt.Errorf("%w: a new-view carries %d view-changes, and %d are needed", ErrBadCertificate, len(m.ViewChanges), r.q)
	}
	seen := map[ReplicaID]bool{}
	for _, vc := range m.ViewChanges {
		if vc.View != m.View {
			return nil, fmt.Errorf("%w: a new-view for %d carries a view-change for %d", ErrBadCertificate, m.View, vc.View)
		}
		if !r.known(vc.From) || seen[vc.From] {
			return nil, fmt.Errorf("%w: a new-view counts %q twice or does not know it", ErrBadCertificate, vc.From)
		}
		seen[vc.From] = true
		if err := r.opts.verify(vc.From, Preimage(vc), vc.Signature); err != nil {
			return nil, fmt.Errorf("%w: a new-view's view-change does not verify: %w", ErrBadCertificate, err)
		}
		if err := validateViewChange(vc, r.q, r.opts.verify, r.known); err != nil {
			return nil, err
		}
	}

	want, minS, maxS, err := r.oSet(m.ViewChanges, m.View)
	if err != nil {
		return nil, err
	}
	if len(want) != len(m.PrePrepares) {
		return nil, fmt.Errorf("%w: a new-view proposes %d assignments, this replica recomputes %d", ErrBadCertificate, len(m.PrePrepares), len(want))
	}
	for i, pp := range m.PrePrepares {
		if pp.Seq != want[i].Seq || pp.Digest != want[i].Digest {
			return nil, fmt.Errorf("%w: a new-view's assignment for sequence %d is not the one this replica recomputes", ErrBadCertificate, pp.Seq)
		}
		if pp.From != m.From || pp.View != m.View {
			return nil, fmt.Errorf("%w: a new-view's assignment is not the new primary's", ErrBadCertificate)
		}
		if err := r.opts.verify(pp.From, Preimage(pp), pp.Signature); err != nil {
			return nil, fmt.Errorf("%w: a new-view's assignment does not verify: %w", ErrBadCertificate, err)
		}
	}
	acts := []Action{Persist{Records: []Record{rec(m)}}}
	return append(acts, r.installNewView(m.View, m.ViewChanges, m.PrePrepares, minS, maxS)...), nil
}

// installNewView enters the view and re-votes on everything it re-proposes.
func (r *Replica) installNewView(v View, v0 []Message, o []Message, minS, maxS Sequence) []Action {
	var acts []Action

	if minS > r.lastStable {
		// The new view's floor is above this replica's: somebody's checkpoint
		// proof says a quorum holds a state it does not. Adopt the proof, then
		// ask the caller for the bytes.
		for _, vc := range v0 {
			if vc.Seq != minS {
				continue
			}
			acts = append(acts, r.adoptStable(minS, vc.Digest, vc.Checkpoints)...)
			if minS > r.lastExecuted {
				acts = append(acts, FetchState{Seq: minS, Digest: vc.Digest, From: certifiersOf(vc.Checkpoints)})
			}
			break
		}
	}

	acts = append(acts, r.enterView(v)...)
	if maxS > r.lastAssigned {
		r.lastAssigned = maxS
	}

	_, high := r.Watermarks()
	for _, pp := range o {
		if pp.Seq <= r.lastStable || pp.Seq > high {
			continue
		}
		r.assigned[pp.Digest] = true
		a, err := r.acceptPrePrepare(pp)
		acts = append(acts, a...)
		if err != nil {
			acts = append(acts, Notice{Err: err})
		}
	}
	acts = append(acts, r.assignPending()...)
	acts = append(acts, r.execute()...)
	return append(acts, r.reconsider()...)
}

// reconsider asks the two questions entering a view may have just answered.
//
// NOTHING ELSE ASKS THEM AGAIN. hearsay and maybeSendNewView are otherwise
// reachable only from an inbound VIEW-CHANGE, so a quorum of them that
// arrived while this replica was still in a lower view sits in the map unread
// — and the view this replica is the primary of never starts, although every
// vote it needs is already here.
func (r *Replica) reconsider() []Action {
	acts := r.hearsay()
	for _, v := range sortedViews(r.viewChanges) {
		if v <= r.view {
			continue
		}
		acts = append(acts, r.maybeSendNewView(v)...)
	}
	return acts
}

func (r *Replica) enterView(v View) []Action {
	r.view = v
	r.viewChanging = false
	r.pendingView = 0
	r.assigned = map[Digest]bool{}
	r.orphans = map[Digest][]Message{}
	r.orphansHeld = 0
	r.log.compactBelow(v, r.q)
	acts := []Action{Persist{Records: []Record{{Kind: RecordViewEntered, View: v}}}}
	if r.timerRunning[TimerNewView] {
		r.timerRunning[TimerNewView] = false
		acts = append(acts, CancelTimer{Which: TimerNewView})
	}
	r.gcViewChanges()
	return acts
}

func certifiersOf(ms []Message) []ReplicaID {
	out := make([]ReplicaID, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.From)
	}
	return out
}

// sortedViews orders a view-keyed map. Iteration order decides which NEW-VIEW
// a replica builds first, and a decision that differs between two runs of one
// binary is not a decision this package is willing to make.
func sortedViews[T any](m map[View]T) []View {
	out := make([]View, 0, len(m))
	for v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
