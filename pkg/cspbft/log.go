package cspbft

// The bounded message log between the water marks. It takes no Scorer: vote counting
// cannot reach ranking. One vote per (sender, kind, view, sequence): a repeat that
// agrees is a retransmission, one that disagrees is equivocation. Nothing below the
// last stable checkpoint.

type logKey struct {
	view View
	seq  Sequence
}

type entry struct {
	view   View
	seq    Sequence
	digest Digest

	prePrepare *Message
	prepares   map[ReplicaID]Message
	commits    map[ReplicaID]Message

	sentPrepare bool
	sentCommit  bool
	committed   bool
}

type messageLog struct {
	entries map[logKey]*entry

	// committed is the entry that reached committed-local at a sequence,
	// whatever view it happened in. Safety is exactly the claim that there is
	// never a second one with a different digest.
	committed map[Sequence]*entry
}

func newLog() *messageLog {
	return &messageLog{
		entries:   map[logKey]*entry{},
		committed: map[Sequence]*entry{},
	}
}

func (l *messageLog) at(v View, n Sequence) *entry {
	return l.entries[logKey{v, n}]
}

func (l *messageLog) ensure(v View, n Sequence) *entry {
	k := logKey{v, n}
	if e, ok := l.entries[k]; ok {
		return e
	}
	e := &entry{
		view:     v,
		seq:      n,
		prepares: map[ReplicaID]Message{},
		commits:  map[ReplicaID]Message{},
	}
	l.entries[k] = e
	return e
}

// accept records a vote, or says why it will not.
//
// It returns the entry so the caller can ask whether the vote completed a
// certificate, and an Equivocation when the sender has just contradicted
// itself — which is the only moment this package ever produces a proof.
func (l *messageLog) accept(m Message) (*entry, *Equivocation, error) {
	e := l.ensure(m.View, m.Seq)
	switch m.Kind {
	case KindPrePrepare:
		if e.prePrepare != nil {
			if e.prePrepare.Digest == m.Digest {
				return e, nil, ErrDuplicateVote
			}
			// A SECOND ASSIGNMENT FOR ONE (view, sequence) IS THE PRIMARY
			// FAULT PBFT EXISTS FOR, and the pair of signed messages proves it
			// to anyone, which is why signatures and not MACs.
			return e, &Equivocation{A: *e.prePrepare, B: m}, ErrEquivocation
		}
		cp := m
		e.prePrepare = &cp
		e.digest = m.Digest
	case KindPrepare:
		if prev, ok := e.prepares[m.From]; ok {
			if prev.Digest == m.Digest {
				return e, nil, ErrDuplicateVote
			}
			return e, &Equivocation{A: prev, B: m}, ErrEquivocation
		}
		e.prepares[m.From] = m
	case KindCommit:
		if prev, ok := e.commits[m.From]; ok {
			if prev.Digest == m.Digest {
				return e, nil, ErrDuplicateVote
			}
			return e, &Equivocation{A: prev, B: m}, ErrEquivocation
		}
		e.commits[m.From] = m
	default:
		return e, nil, ErrMalformed
	}
	return e, nil, nil
}

// discardThrough drops everything at or below the new low water mark. The
// committed index keeps its entries for executed sequences only long enough to
// be discarded here too: a checkpoint is stable exactly when a quorum has the
// state, so nothing below it can still be needed to decide anything.
func (l *messageLog) discardThrough(n Sequence) {
	for k := range l.entries {
		if k.seq <= n {
			delete(l.entries, k)
		}
	}
	for seq := range l.committed {
		if seq <= n {
			delete(l.committed, seq)
		}
	}
}

// compactBelow drops what entering a view made unreachable: older-view entries can
// gain no votes, and only the highest prepared view per sequence is ever used. At
// most one old entry per sequence survives, bounding the log across long runs of
// view changes.
func (l *messageLog) compactBelow(v View, q int) {
	best := map[Sequence]View{}
	for k, e := range l.entries {
		if k.view >= v || !prepared(e, q) {
			continue
		}
		if cur, ok := best[k.seq]; !ok || k.view > cur {
			best[k.seq] = k.view
		}
	}
	for k, e := range l.entries {
		if k.view >= v || e.committed {
			continue
		}
		if at, ok := best[k.seq]; ok && at == k.view {
			continue
		}
		delete(l.entries, k)
	}
}

// preparedCertificates is P for a VIEW-CHANGE: for every sequence above the
// last stable checkpoint that this replica has prepared, the highest-view
// certificate it holds.
func (l *messageLog) preparedCertificates(above Sequence, q int) []Certificate {
	best := map[Sequence]*entry{}
	for _, e := range l.entries {
		if e.seq <= above || !prepared(e, q) {
			continue
		}
		if cur, ok := best[e.seq]; !ok || e.view > cur.view {
			best[e.seq] = e
		}
	}
	out := make([]Certificate, 0, len(best))
	for _, e := range best {
		out = append(out, e.certificate(q))
	}
	sortCertificates(out)
	return out
}

// certificate materialises a prepared certificate from an entry, carrying
// exactly q-1 prepares — no more, because a VIEW-CHANGE is already the
// heaviest message in the protocol and every extra signature in it is a
// signature every recipient verifies.
func (e *entry) certificate(q int) Certificate {
	c := Certificate{PrePrepare: *e.prePrepare}
	for _, id := range sortedIDs(e.prepares) {
		if len(c.Prepares) == q-1 {
			break
		}
		p := e.prepares[id]
		if p.Digest == e.digest && p.From != e.prePrepare.From {
			c.Prepares = append(c.Prepares, p)
		}
	}
	return c
}
