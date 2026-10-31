package kvdb

import (
	"maps"
	"slices"
	"sort"
	"sync/atomic"

	pb "github.com/taubyte/tau/pkg/kvdb/pb"
)

// A span is the counters (lo, hi] of one incarnation. The author's counters
// in it other than the ones it used name nothing, so covering them is
// harmless: a chain links each delta to its author's previous counter, and a
// counter the author skipped is not a gap.
type span struct{ lo, hi uint64 }

func (s span) touches(o span) bool { return s.lo <= o.hi && o.lo <= s.hi }

// known is what a context holds about one incarnation: the counters it
// covers, as sorted spans that neither overlap nor touch, the latest authored
// time of anything merged about it (changed), and the latest time anything of
// it is still needed (expires). Times are milliseconds since the epoch.
//
// The first span is the incarnation's entry, (base, counter]; the others are
// its cloud, covered past a gap. A dot at or below base is not covered: its
// incarnation was first seen above it, or was forgotten and seen again.
type known struct {
	spans   []span
	changed uint64
	expires uint64
	// gen is the generation of the context that may change k in place. A
	// clone shares its entries, and copies one before it changes it.
	gen uint64
	// buf holds the spans of an entry own made, while they are few, in the
	// entry's allocation.
	buf [2]span
}

func (k *known) covers(counter uint64) bool {
	i := sort.Search(len(k.spans), func(i int) bool { return k.spans[i].hi >= counter })
	return i < len(k.spans) && k.spans[i].lo < counter
}

// insert adds s to the spans, joining every span it overlaps or touches. It
// changes k's spans in place: k must be its context's own.
func (k *known) insert(s span) {
	if s.hi <= s.lo {
		return
	}
	// The spans below i end before s starts; those in [i, j) touch it.
	i := sort.Search(len(k.spans), func(i int) bool { return k.spans[i].hi >= s.lo })
	j := i
	for ; j < len(k.spans) && k.spans[j].touches(s); j++ {
		s = span{lo: min(k.spans[j].lo, s.lo), hi: max(k.spans[j].hi, s.hi)}
	}
	k.spans = slices.Replace(k.spans, i, j, s)
}

func (k *known) stamp(changed, expires uint64) {
	k.changed = max(k.changed, changed)
	k.expires = max(k.expires, expires)
}

// causalContext is the set of dots a state has seen, per incarnation, with
// how recent and how long-lived what it knows of each one is.
//
// It is the context of the add-wins observed-remove set: an element whose dot
// the context covers and that the state does not hold was removed. An
// incarnation the context no longer knows has been forgotten by expiry (see
// retire); its elements are ancient and are kept under their own dots.
type causalContext struct {
	inc map[string]*known
	gen uint64
}

// contextGens numbers contexts, so an entry names the one it belongs to
// without keeping it.
var contextGens atomic.Uint64

func newCausalContext() *causalContext {
	return &causalContext{inc: map[string]*known{}, gen: contextGens.Add(1)}
}

func (c *causalContext) covers(d dot) bool {
	if d.isZero() {
		return true
	}
	k := c.inc[d.replica]
	return k != nil && k.covers(d.counter)
}

// knows reports whether the context has an entry for the incarnation.
func (c *causalContext) knows(replica string) bool {
	return c.inc[replica] != nil
}

// counter is the end of the incarnation's entry: the counter its chain is
// covered up to from its base, 0 when the context does not know it.
func (c *causalContext) counter(replica string) uint64 {
	k := c.inc[replica]
	if k == nil {
		return 0
	}
	return k.spans[0].hi
}

// add records d, whose author's previous counter was prev, authored at at and
// needed until expires. An incarnation it does not know yet is opened at
// prev: the dots below are ancient here.
func (c *causalContext) add(d dot, prev, at, expires uint64) {
	if d.isZero() {
		return
	}
	if prev >= d.counter {
		prev = d.counter - 1
	}
	k := c.own(d.replica)
	k.insert(span{lo: prev, hi: d.counter})
	k.stamp(at, expires)
}

// own is c's entry for replica, made c's own: a new one if c has none, a
// copy if c shares it.
func (c *causalContext) own(replica string) *known {
	k := c.inc[replica]
	switch {
	case k == nil:
		k = &known{gen: c.gen}
		k.spans = k.buf[:0]
		c.inc[replica] = k
	case k.gen != c.gen:
		n := &known{changed: k.changed, expires: k.expires, gen: c.gen}
		n.spans = append(n.buf[:0], k.spans...)
		k = n
		c.inc[replica] = k
	}
	return k
}

// touch records that something authored at at, needed until expires, changed
// what is known of an incarnation the context knows: a removal of one of its
// dots.
func (c *causalContext) touch(replica string, at, expires uint64) {
	if k := c.inc[replica]; k != nil && (k.changed < at || k.expires < expires) {
		c.own(replica).stamp(at, expires)
	}
}

// merge joins o into c: coverage by union, the stamps by max.
func (c *causalContext) merge(o *causalContext) {
	for r, ok := range o.inc {
		k := c.own(r)
		for _, s := range ok.spans {
			k.insert(s)
		}
		k.stamp(ok.changed, ok.expires)
	}
}

// clone is a context equal to c that shares c's entries until it changes
// them.
func (c *causalContext) clone() *causalContext {
	n := &causalContext{inc: make(map[string]*known, len(c.inc)), gen: contextGens.Add(1)}
	maps.Copy(n.inc, c.inc)
	return n
}

func (c *causalContext) replicas() []string { return c.appendReplicas(nil) }

// appendReplicas appends c's replicas to out, sorted.
func (c *causalContext) appendReplicas(out []string) []string {
	start := len(out)
	for r := range c.inc {
		out = append(out, r)
	}
	slices.Sort(out[start:])
	return out
}

func (c *causalContext) toPB() *pb.Context {
	out := &pb.Context{}
	for _, r := range c.replicas() {
		k := c.inc[r]
		out.Vector = append(out.Vector, &pb.Entry{
			Replica: []byte(r), Base: k.spans[0].lo, Counter: k.spans[0].hi,
			Changed: k.changed, Expires: k.expires,
		})
		for _, s := range k.spans[1:] {
			out.Cloud = append(out.Cloud, &pb.Span{Replica: []byte(r), Lo: s.lo, Hi: s.hi})
		}
	}
	return out
}

// contextFromPB reads a context. A span of an incarnation with no entry is
// dropped: no context this package writes has one.
func contextFromPB(p *pb.Context) *causalContext {
	c := newCausalContext()
	for _, e := range p.GetVector() {
		if e.GetCounter() <= e.GetBase() {
			continue
		}
		k := c.inc[string(e.GetReplica())]
		if k == nil {
			k = &known{}
			c.inc[string(e.GetReplica())] = k
		}
		k.insert(span{lo: e.GetBase(), hi: e.GetCounter()})
		k.stamp(e.GetChanged(), e.GetExpires())
	}
	for _, s := range p.GetCloud() {
		if k := c.inc[string(s.GetReplica())]; k != nil {
			k.insert(span{lo: s.GetLo(), hi: s.GetHi()})
		}
	}
	return c
}
