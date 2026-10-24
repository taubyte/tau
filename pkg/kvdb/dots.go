package kvdb

import (
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strings"

	pb "github.com/taubyte/tau/pkg/kvdb/pb"
)

// A dot names one delta: the incarnation that wrote it and that incarnation's
// counter for it. Counters start at 1, so the zero dot names nothing.
//
// An incarnation is one open of one store: the node's peer id followed by a
// nonce drawn when the store is opened. It is never persisted for reuse. A
// store restored from a backup is byte-identical to the store it was copied
// from, so nothing local could tell the two apart; drawing the nonce on every
// open is what makes reusing a dot impossible rather than unlikely.
type dot struct {
	replica string
	counter uint64
}

func (d dot) isZero() bool { return d.counter == 0 }

func (d dot) String() string {
	return fmt.Sprintf("%x:%d", d.replica, d.counter)
}

var segEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// segment encodes the dot as one datastore key segment that sorts by replica
// and then by counter: the counter is zero-padded to 20 digits so byte order
// is numeric order.
func (d dot) segment() string {
	return replicaSegment(d.replica) + "." + fmt.Sprintf("%020d", d.counter)
}

func replicaSegment(replica string) string {
	return "r" + segEncoding.EncodeToString([]byte(replica))
}

func parseDotSegment(s string) (dot, error) {
	r, c, ok := strings.Cut(s, ".")
	if !ok || !strings.HasPrefix(r, "r") {
		return dot{}, fmt.Errorf("bad dot segment %q", s)
	}
	replica, err := segEncoding.DecodeString(r[1:])
	if err != nil {
		return dot{}, fmt.Errorf("bad dot segment %q: %w", s, err)
	}
	var counter uint64
	if _, err := fmt.Sscanf(c, "%d", &counter); err != nil || len(c) != 20 {
		return dot{}, fmt.Errorf("bad dot segment %q", s)
	}
	return dot{replica: string(replica), counter: counter}, nil
}

// keySegment encodes a user key as one segment, so that the markers of "/a"
// are never under the prefix of the markers of "/a/b".
func keySegment(key string) string {
	return "k" + segEncoding.EncodeToString([]byte(key))
}

func parseKeySegment(s string) (string, error) {
	if !strings.HasPrefix(s, "k") {
		return "", fmt.Errorf("bad key segment %q", s)
	}
	b, err := segEncoding.DecodeString(s[1:])
	if err != nil {
		return "", fmt.Errorf("bad key segment %q: %w", s, err)
	}
	return string(b), nil
}

func (d dot) toPB() *pb.Dot {
	if d.isZero() {
		return nil
	}
	return &pb.Dot{Replica: []byte(d.replica), Counter: d.counter}
}

func dotFromPB(p *pb.Dot) dot {
	return dot{replica: string(p.GetReplica()), counter: p.GetCounter()}
}

// encodeDot is the value form of a dot: varint counter, then the replica.
func encodeDot(d dot) []byte {
	buf := binary.AppendUvarint(nil, d.counter)
	return append(buf, d.replica...)
}

func decodeDot(b []byte) (dot, error) {
	c, n := binary.Uvarint(b)
	if n <= 0 {
		return dot{}, errors.New("error decoding dot")
	}
	return dot{counter: c, replica: string(b[n:])}, nil
}

// causalContext is the set of dots a state has seen: per incarnation, the
// counter up to which its chain is covered, plus the dots covered past a gap
// in it. A chain is the incarnation's deltas linked by their prev counters,
// so a counter the author skipped is not a gap.
//
// It is the context of the add-wins observed-remove set: an element whose dot
// the context covers and that the state does not hold was removed.
type causalContext struct {
	vector map[string]uint64
	cloud  map[string]map[uint64]uint64 // replica -> counter -> prev
}

func newCausalContext() *causalContext {
	return &causalContext{vector: map[string]uint64{}, cloud: map[string]map[uint64]uint64{}}
}

func (c *causalContext) covers(d dot) bool {
	if d.isZero() {
		return true
	}
	if d.counter <= c.vector[d.replica] {
		return true
	}
	_, ok := c.cloud[d.replica][d.counter]
	return ok
}

// add records d, whose author's previous counter was prev, and absorbs every
// cloud dot the vector now reaches.
func (c *causalContext) add(d dot, prev uint64) {
	if c.covers(d) {
		return
	}
	if prev <= c.vector[d.replica] {
		c.vector[d.replica] = d.counter
	} else {
		cl := c.cloud[d.replica]
		if cl == nil {
			cl = map[uint64]uint64{}
			c.cloud[d.replica] = cl
		}
		cl[d.counter] = prev
	}
	c.absorb(d.replica)
}

func (c *causalContext) absorb(replica string) {
	cl := c.cloud[replica]
	if len(cl) == 0 {
		delete(c.cloud, replica)
		return
	}
	counters := make([]uint64, 0, len(cl))
	for k := range cl {
		counters = append(counters, k)
	}
	sort.Slice(counters, func(i, j int) bool { return counters[i] < counters[j] })
	v := c.vector[replica]
	for _, k := range counters {
		switch {
		case k <= v:
			delete(cl, k)
		case cl[k] <= v:
			v = k
			delete(cl, k)
		}
	}
	if v > 0 {
		c.vector[replica] = v
	}
	if len(cl) == 0 {
		delete(c.cloud, replica)
	}
}

// merge joins o into c.
func (c *causalContext) merge(o *causalContext) {
	touched := map[string]struct{}{}
	for r, v := range o.vector {
		if v > c.vector[r] {
			c.vector[r] = v
		}
		touched[r] = struct{}{}
	}
	for r, cl := range o.cloud {
		mine := c.cloud[r]
		if mine == nil {
			mine = map[uint64]uint64{}
			c.cloud[r] = mine
		}
		for k, p := range cl {
			mine[k] = p
		}
		touched[r] = struct{}{}
	}
	for r := range touched {
		c.absorb(r)
	}
}

func (c *causalContext) clone() *causalContext {
	n := newCausalContext()
	n.merge(c)
	return n
}

func (c *causalContext) toPB() *pb.Context {
	out := &pb.Context{}
	replicas := make([]string, 0, len(c.vector))
	for r := range c.vector {
		replicas = append(replicas, r)
	}
	sort.Strings(replicas)
	for _, r := range replicas {
		out.Vector = append(out.Vector, &pb.Dot{Replica: []byte(r), Counter: c.vector[r]})
	}
	replicas = replicas[:0]
	for r := range c.cloud {
		replicas = append(replicas, r)
	}
	sort.Strings(replicas)
	for _, r := range replicas {
		counters := make([]uint64, 0, len(c.cloud[r]))
		for k := range c.cloud[r] {
			counters = append(counters, k)
		}
		sort.Slice(counters, func(i, j int) bool { return counters[i] < counters[j] })
		for _, k := range counters {
			out.Cloud = append(out.Cloud, &pb.CloudDot{Dot: &pb.Dot{Replica: []byte(r), Counter: k}, Prev: c.cloud[r][k]})
		}
	}
	return out
}

func contextFromPB(p *pb.Context) *causalContext {
	c := newCausalContext()
	o := newCausalContext()
	for _, v := range p.GetVector() {
		if v.GetCounter() > o.vector[string(v.GetReplica())] {
			o.vector[string(v.GetReplica())] = v.GetCounter()
		}
	}
	for _, cd := range p.GetCloud() {
		d := dotFromPB(cd.GetDot())
		if d.isZero() {
			continue
		}
		cl := o.cloud[d.replica]
		if cl == nil {
			cl = map[uint64]uint64{}
			o.cloud[d.replica] = cl
		}
		cl[d.counter] = cd.GetPrev()
	}
	c.merge(o)
	return c
}
