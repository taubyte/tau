package kvdb

import (
	"sort"

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
}

func (k *known) covers(counter uint64) bool {
	i := sort.Search(len(k.spans), func(i int) bool { return k.spans[i].hi >= counter })
	return i < len(k.spans) && k.spans[i].lo < counter
}

// insert adds s to the spans, joining every span it overlaps or touches.
func (k *known) insert(s span) {
	if s.hi <= s.lo {
		return
	}
	out := make([]span, 0, len(k.spans)+1)
	placed := false
	for _, o := range k.spans {
		switch {
		case o.touches(s):
			s = span{lo: min(o.lo, s.lo), hi: max(o.hi, s.hi)}
		case o.hi < s.lo:
			out = append(out, o)
		default:
			if !placed {
				out = append(out, s)
				placed = true
			}
			out = append(out, o)
		}
	}
	if !placed {
		out = append(out, s)
		sort.Slice(out, func(i, j int) bool { return out[i].lo < out[j].lo })
	}
	k.spans = out
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
}

func newCausalContext() *causalContext {
	return &causalContext{inc: map[string]*known{}}
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
	k := c.inc[d.replica]
	if k == nil {
		k = &known{}
		c.inc[d.replica] = k
	}
	k.insert(span{lo: prev, hi: d.counter})
	k.stamp(at, expires)
}

// touch records that something authored at at, needed until expires, changed
// what is known of an incarnation the context knows: a removal of one of its
// dots.
func (c *causalContext) touch(replica string, at, expires uint64) {
	if k := c.inc[replica]; k != nil {
		k.stamp(at, expires)
	}
}

// merge joins o into c: coverage by union, the stamps by max.
func (c *causalContext) merge(o *causalContext) {
	for r, ok := range o.inc {
		k := c.inc[r]
		if k == nil {
			k = &known{}
			c.inc[r] = k
		}
		for _, s := range ok.spans {
			k.insert(s)
		}
		k.stamp(ok.changed, ok.expires)
	}
}

func (c *causalContext) clone() *causalContext {
	n := newCausalContext()
	n.merge(c)
	return n
}

func (c *causalContext) replicas() []string {
	out := make([]string, 0, len(c.inc))
	for r := range c.inc {
		out = append(out, r)
	}
	sort.Strings(out)
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
