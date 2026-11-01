package kvdb

import (
	"maps"
	"slices"
	"sort"
	"sync/atomic"

	pb "github.com/taubyte/tau/pkg/kvdb/pb"
)

// A span is the counters (lo, hi] of one incarnation. Counters the author
// skipped name nothing, so covering them is harmless.
type span struct{ lo, hi uint64 }

func (s span) touches(o span) bool { return s.lo <= o.hi && o.lo <= s.hi }

// known is what a context holds about one incarnation: its covered counters
// as sorted spans that neither overlap nor touch, the latest authored time
// merged about it (changed) and the latest time any of it is needed
// (expires), in milliseconds since the epoch. The first span is its entry,
// (base, counter]; the rest are covered past a gap. A dot at or below base is
// not covered.
type known struct {
	spans   []span
	changed uint64
	expires uint64
	// gen is the generation of the context that may change k in place; a clone
	// copies an entry before changing it.
	gen uint64
	// buf holds a few spans in the entry's own allocation.
	buf [2]span
}

func (k *known) covers(counter uint64) bool {
	i := sort.Search(len(k.spans), func(i int) bool { return k.spans[i].hi >= counter })
	return i < len(k.spans) && k.spans[i].lo < counter
}

// insert joins every span s overlaps or touches. It changes k in place: k must
// be its context's own.
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

// causalContext is the dots a state has seen, per incarnation: the context of
// the add-wins observed-remove set, where an element whose dot it covers and
// the state does not hold was removed. An incarnation it no longer knows was
// forgotten by expiry; its elements are ancient and kept under their own dots.
type causalContext struct {
	inc map[string]*known
	gen uint64
}

// contextGens lets an entry name its context without keeping it.
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

func (c *causalContext) knows(replica string) bool {
	return c.inc[replica] != nil
}

// counter is the end of the incarnation's entry, 0 when unknown.
func (c *causalContext) counter(replica string) uint64 {
	k := c.inc[replica]
	if k == nil {
		return 0
	}
	return k.spans[0].hi
}

// An incarnation c does not know yet is opened at prev: the dots below are
// ancient here.
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

// own makes c's entry for replica its own: a new one if absent, a copy if
// shared.
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

func (c *causalContext) touch(replica string, at, expires uint64) {
	if k := c.inc[replica]; k != nil && (k.changed < at || k.expires < expires) {
		c.own(replica).stamp(at, expires)
	}
}

// merge joins o into c: coverage by union, stamps by max.
func (c *causalContext) merge(o *causalContext) {
	for r, ok := range o.inc {
		k := c.own(r)
		for _, s := range ok.spans {
			k.insert(s)
		}
		k.stamp(ok.changed, ok.expires)
	}
}

// clone shares c's entries until it changes them.
func (c *causalContext) clone() *causalContext {
	n := &causalContext{inc: make(map[string]*known, len(c.inc)), gen: contextGens.Add(1)}
	maps.Copy(n.inc, c.inc)
	return n
}

func (c *causalContext) replicas() []string { return c.appendReplicas(nil) }

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

// A span of an incarnation with no entry is dropped: no context this package
// writes has one.
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
