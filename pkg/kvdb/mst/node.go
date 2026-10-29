package mst

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"

	dag "github.com/ipfs/boxo/ipld/merkledag"
	cid "github.com/ipfs/go-cid"
	ipld "github.com/ipfs/go-ipld-format"
	pb "github.com/taubyte/tau/pkg/kvdb/mst/pb"
	"google.golang.org/protobuf/proto"
)

// Format is the format profile this package reads and writes.
const Format = 1

const levelDomain = "tau/mst/v1/level"

// noLevel bounds a root, which may be at any level.
const noLevel = math.MaxUint32

var (
	ErrMalformed    = errors.New("mst: malformed node")
	ErrFormat       = errors.New("mst: unknown format")
	ErrUnsorted     = errors.New("mst: keys out of order")
	ErrDuplicateKey = errors.New("mst: duplicate key")
)

// Level is the level of key in format profile 1.
func Level(key []byte) uint32 {
	var buf [128]byte
	in := buf[:0]
	if len(levelDomain)+len(key) <= len(buf) {
		in = append(append(in, levelDomain...), key...)
	} else {
		in = append(append(make([]byte, 0, len(levelDomain)+len(key)), levelDomain...), key...)
	}
	sum := sha256.Sum256(in)
	var l uint32
	for _, b := range sum {
		if b == 0 {
			l += 2
			continue
		}
		if b < 0x10 {
			l++
		}
		break
	}
	return l
}

// A Summarizer gives each subtree a summary of its entries. Leaf summarises
// one entry and Merge two adjacent runs of entries, left before right. Merge
// must be associative, both must be pure functions of their arguments, and
// what they return must not share memory with those arguments, which they
// must not modify.
type Summarizer interface {
	Leaf(key, value []byte) []byte
	Merge(left, right []byte) []byte
}

type entry struct {
	key, val []byte
}

// A ref is a subtree slot: its root, undefined for none, and its summary;
// or, while a change is under way, the node it made and has not encoded.
type ref struct {
	c   cid.Cid
	sum []byte
	n   *node
}

func (r ref) present() bool { return r.c.Defined() || r.n != nil }

// is reports whether r and o are the same subtree.
func (r ref) is(o ref) bool { return r.c == o.c && r.n == o.n }

// A node in memory has one more kid than it has entries: kids[i] holds the
// keys between entries[i-1] and entries[i].
type node struct {
	level   uint32
	entries []entry
	kids    []ref
}

// search returns the index of key among the entries, or the slot it would
// be in.
func (n *node) search(key []byte) (int, bool) {
	lo, hi := 0, len(n.entries)
	for lo < hi {
		m := int(uint(lo+hi) >> 1)
		switch c := bytes.Compare(n.entries[m].key, key); {
		case c == 0:
			return m, true
		case c < 0:
			lo = m + 1
		default:
			hi = m
		}
	}
	return lo, false
}

func (n *node) summary(s Summarizer) []byte {
	if s == nil {
		return nil
	}
	var acc []byte
	have := false
	add := func(x []byte) {
		if have {
			acc = s.Merge(acc, x)
		} else {
			acc, have = x, true
		}
	}
	for i, e := range n.entries {
		if n.kids[i].c.Defined() {
			add(n.kids[i].sum)
		}
		add(s.Leaf(e.key, e.val))
	}
	if last := n.kids[len(n.entries)]; last.c.Defined() {
		add(last.sum)
	}
	return acc
}

func sharedPrefix(a, b []byte) int {
	n := min(len(a), len(b))
	for i := range n {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

var marshal = proto.MarshalOptions{Deterministic: true}

// encode makes the block for n.
func encode(n *node) (*dag.ProtoNode, error) {
	es := make([]pb.Entry, len(n.entries))
	msg := &pb.Node{
		Format:      Format,
		Level:       n.level,
		Left:        n.kids[0].c.Defined(),
		LeftSummary: n.kids[0].sum,
		Entries:     make([]*pb.Entry, len(n.entries)),
	}
	var prev []byte
	for i, e := range n.entries {
		shared := sharedPrefix(prev, e.key)
		es[i].Shared = uint32(shared)
		es[i].Suffix = e.key[shared:]
		es[i].Value = e.val
		es[i].Right = n.kids[i+1].c.Defined()
		es[i].RightSummary = n.kids[i+1].sum
		msg.Entries[i] = &es[i]
		prev = e.key
	}
	data, err := marshal.Marshal(msg)
	if err != nil {
		return nil, err
	}
	nd := dag.NodeWithData(data)
	for _, k := range n.kids {
		if k.c.Defined() {
			if err := nd.AddRawLink("", &ipld.Link{Cid: k.c}); err != nil {
				return nil, err
			}
		}
	}
	return nd, nd.SetCidBuilder(dag.V1CidPrefix())
}

func protoNode(nd ipld.Node) (*dag.ProtoNode, error) {
	if pn, ok := nd.(*dag.ProtoNode); ok {
		return pn, nil
	}
	return dag.DecodeProtobuf(nd.RawData())
}

func malformed(c cid.Cid, format string, args ...any) error {
	return fmt.Errorf("%w %s: %s", ErrMalformed, c, fmt.Sprintf(format, args...))
}

// decode reads the node in nd, which must sit below a node of level below.
func decode(nd ipld.Node, below uint32) (*node, error) {
	c := nd.Cid()
	pn, err := protoNode(nd)
	if err != nil {
		return nil, malformed(c, "%s", err)
	}
	var msg pb.Node
	if err := proto.Unmarshal(pn.Data(), &msg); err != nil {
		return nil, malformed(c, "%s", err)
	}
	if msg.Format != Format {
		return nil, fmt.Errorf("%w %d in %s", ErrFormat, msg.Format, c)
	}
	if len(msg.ProtoReflect().GetUnknown()) > 0 {
		return nil, malformed(c, "unknown fields")
	}
	if len(msg.Entries) == 0 {
		return nil, malformed(c, "no entries")
	}
	if msg.Level >= below {
		return nil, malformed(c, "level %d under level %d", msg.Level, below)
	}
	size := 0
	for _, e := range msg.Entries {
		if len(e.ProtoReflect().GetUnknown()) > 0 {
			return nil, malformed(c, "unknown fields")
		}
		size += int(e.Shared) + len(e.Suffix)
	}
	links := pn.Links()
	n := &node{
		level:   msg.Level,
		entries: make([]entry, len(msg.Entries)),
		kids:    make([]ref, len(msg.Entries)+1),
	}
	li := 0
	slot := func(i int, present bool, sum []byte) error {
		if !present {
			if len(sum) > 0 {
				return malformed(c, "summary of no subtree")
			}
			return nil
		}
		if li == len(links) {
			return malformed(c, "more subtrees than links")
		}
		n.kids[i] = ref{c: links[li].Cid, sum: sum}
		li++
		return nil
	}
	if err := slot(0, msg.Left, msg.LeftSummary); err != nil {
		return nil, err
	}
	keys := make([]byte, 0, size)
	var prev []byte
	for i, e := range msg.Entries {
		if int(e.Shared) > len(prev) || (i == 0 && e.Shared > 0) {
			return nil, malformed(c, "entry %d shares more than the previous key", i)
		}
		start := len(keys)
		keys = append(append(keys, prev[:e.Shared]...), e.Suffix...)
		key := keys[start:len(keys):len(keys)]
		if i > 0 {
			switch bytes.Compare(prev, key) {
			case 0:
				return nil, fmt.Errorf("%w in %s", ErrDuplicateKey, c)
			case 1:
				return nil, fmt.Errorf("%w in %s", ErrUnsorted, c)
			}
		}
		n.entries[i] = entry{key: key, val: e.Value}
		if err := slot(i+1, e.Right, e.RightSummary); err != nil {
			return nil, err
		}
		prev = key
	}
	if li != len(links) {
		return nil, malformed(c, "%d links for %d subtrees", len(links), li)
	}
	return n, nil
}
