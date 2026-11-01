package statetree

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"slices"

	blocks "github.com/ipfs/go-block-format"
	cid "github.com/ipfs/go-cid"
	pb "github.com/taubyte/tau/pkg/kvdb/statetree/pb"
)

const Format = 2

const levelDomain = "tau/mst/v1/level"

// A root may be at any level.
const noLevel = math.MaxUint32

var (
	ErrMalformed    = errors.New("statetree: malformed node")
	ErrFormat       = errors.New("statetree: unknown format")
	ErrUnsorted     = errors.New("statetree: keys out of order")
	ErrDuplicateKey = errors.New("statetree: duplicate key")
)

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

// A Summarizer folds entries into subtree summaries. Leaf appends one entry's
// summary to dst, Merge that of two adjacent runs, left first. Both must be
// pure and leave their arguments unmodified; Merge must be associative and
// read left and right before writing, as dst may alias either.
type Summarizer interface {
	Leaf(dst, key, value []byte) []byte
	Merge(dst, left, right []byte) []byte
}

type entry struct {
	key, val []byte
}

// A ref is a subtree's root and summary, undefined for none, or while a change
// is under way the unencoded node it made.
type ref struct {
	c   cid.Cid
	sum []byte
	n   *node
}

func (r ref) present() bool { return r.c.Defined() || r.n != nil }

func (r ref) is(o ref) bool { return r.c == o.c && r.n == o.n }

// A node in memory has one more kid than it has entries: kids[i] holds the
// keys between entries[i-1] and entries[i].
type node struct {
	level   uint32
	entries []entry
	kids    []ref
}

// A node is made with its entries and kids in one allocation, sized to the
// smallest class that holds them; past the largest they are made apart.
type (
	node8 struct {
		n node
		e [8]entry
		k [9]ref
	}
	node16 struct {
		n node
		e [16]entry
		k [17]ref
	}
	node32 struct {
		n node
		e [32]entry
		k [33]ref
	}
	node64 struct {
		n node
		e [64]entry
		k [65]ref
	}
)

func nodeRoom(entries int) int {
	switch {
	case entries <= 8:
		return 8
	case entries <= 16:
		return 16
	case entries <= 32:
		return 32
	case entries <= 64:
		return 64
	}
	return entries
}

func newNode(level uint32, entries int) *node {
	switch nodeRoom(entries) {
	case 8:
		s := new(node8)
		s.n = node{level: level, entries: s.e[:entries], kids: s.k[:entries+1]}
		return &s.n
	case 16:
		s := new(node16)
		s.n = node{level: level, entries: s.e[:entries], kids: s.k[:entries+1]}
		return &s.n
	case 32:
		s := new(node32)
		s.n = node{level: level, entries: s.e[:entries], kids: s.k[:entries+1]}
		return &s.n
	case 64:
		s := new(node64)
		s.n = node{level: level, entries: s.e[:entries], kids: s.k[:entries+1]}
		return &s.n
	}
	return &node{level: level, entries: make([]entry, entries), kids: make([]ref, entries+1)}
}

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
	// acc holds the summary so far, its first sum bytes (none while sum is
	// -1), then room for the next entry's.
	var acc []byte
	sum := -1
	take := func(x []byte) {
		if sum < 0 {
			acc = append(acc[:0], x...)
		} else {
			acc = s.Merge(acc[:0], acc[:sum], x)
		}
		sum = len(acc)
	}
	for i, e := range n.entries {
		if n.kids[i].c.Defined() {
			take(n.kids[i].sum)
		}
		base := max(sum, 0)
		acc = s.Leaf(acc[:base], e.key, e.val)
		take(acc[base:])
	}
	if last := n.kids[len(n.entries)]; last.c.Defined() {
		take(last.sum)
	}
	return acc[:sum:sum]
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

// encode makes the block for n. Its message comes from a pool and borrows
// n's bytes until it is marshalled.
func encode(n *node) (*Block, error) {
	msg := pb.NodeFromVTPool()
	defer msg.ReturnToVTPool()
	msg.Format = Format
	msg.Level = n.level
	msg.Left = n.kids[0].c.Defined()
	msg.LeftSummary = n.kids[0].sum
	msg.Entries = slices.Grow(msg.Entries[:0], len(n.entries))[:len(n.entries)]
	var prev []byte
	for i, e := range n.entries {
		pe := msg.Entries[i]
		if pe == nil {
			pe = pb.EntryFromVTPool()
			msg.Entries[i] = pe
		}
		shared := sharedPrefix(prev, e.key)
		pe.Shared = uint32(shared)
		pe.Suffix = e.key[shared:]
		pe.Value = e.val
		pe.Right = n.kids[i+1].c.Defined()
		pe.RightSummary = n.kids[i+1].sum
		prev = e.key
	}
	data, err := msg.MarshalVT()
	if err != nil {
		return nil, err
	}
	var buf [32]cid.Cid
	links := buf[:0]
	for _, k := range n.kids {
		if k.c.Defined() {
			links = append(links, k.c)
		}
	}
	return NewBlock(data, links)
}

func malformed(c cid.Cid, format string, args ...any) error {
	return fmt.Errorf("%w %s: %s", ErrMalformed, c, fmt.Sprintf(format, args...))
}

// nd must sit under a node of level below.
func decode(nd blocks.Block, below uint32) (*node, error) {
	n, _, err := decodeNode(nd, below, nil)
	return n, err
}

// decodeNode is decode, but when own accepts the node's level and footprint
// the node copies its bytes into one buffer of its own, and that cost is
// returned; otherwise 0.
func decodeNode(nd blocks.Block, below uint32, own func(level uint32, size int) bool) (*node, int, error) {
	c := nd.Cid()
	data, links, err := DecodeBlock(nd, nil)
	if err != nil {
		return nil, 0, malformed(c, "%s", err)
	}
	// The message aliases the block's data. Blocks never change and the node
	// keeps only clipped slices, so no append reaches past them; the message
	// returns to the pool when decode does.
	msg := pb.NodeFromVTPool()
	defer msg.ReturnToVTPool()
	if err := msg.UnmarshalVTUnsafe(data); err != nil {
		return nil, 0, malformed(c, "%s", err)
	}
	if msg.Format != Format {
		return nil, 0, fmt.Errorf("%w %d in %s", ErrFormat, msg.Format, c)
	}
	if len(msg.ProtoReflect().GetUnknown()) > 0 {
		return nil, 0, malformed(c, "unknown fields")
	}
	if len(msg.Entries) == 0 {
		return nil, 0, malformed(c, "no entries")
	}
	if msg.Level >= below {
		return nil, 0, malformed(c, "level %d under level %d", msg.Level, below)
	}
	size, owned := 0, len(msg.LeftSummary)
	for _, e := range msg.Entries {
		if len(e.ProtoReflect().GetUnknown()) > 0 {
			return nil, 0, malformed(c, "unknown fields")
		}
		size += int(e.Shared) + len(e.Suffix)
		owned += len(e.Value) + len(e.RightSummary)
	}
	cost := 0
	if own != nil {
		cids := 0
		for _, l := range links {
			cids += len(l.KeyString())
		}
		if cost = footprint(nodeRoom(len(msg.Entries)), size+owned, cids); own(msg.Level, cost) {
			size += owned + cids
		} else {
			cost = 0
		}
	}
	n := newNode(msg.Level, len(msg.Entries))
	buf := make([]byte, 0, size)
	held := func(b []byte) []byte {
		if cost == 0 {
			return slices.Clip(b)
		}
		start := len(buf)
		buf = append(buf, b...)
		return buf[start:len(buf):len(buf)]
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
		// A link aliases the block; a node that owns its bytes takes it over its own.
		l := links[li]
		if cost != 0 {
			l = CastCid(held(cidBytes(l)))
		}
		n.kids[i] = ref{c: l, sum: held(sum)}
		li++
		return nil
	}
	if err := slot(0, msg.Left, msg.LeftSummary); err != nil {
		return nil, 0, err
	}
	var prev []byte
	for i, e := range msg.Entries {
		if int(e.Shared) > len(prev) || (i == 0 && e.Shared > 0) {
			return nil, 0, malformed(c, "entry %d shares more than the previous key", i)
		}
		start := len(buf)
		buf = append(append(buf, prev[:e.Shared]...), e.Suffix...)
		key := buf[start:len(buf):len(buf)]
		if i > 0 {
			switch bytes.Compare(prev, key) {
			case 0:
				return nil, 0, fmt.Errorf("%w in %s", ErrDuplicateKey, c)
			case 1:
				return nil, 0, fmt.Errorf("%w in %s", ErrUnsorted, c)
			}
		}
		n.entries[i] = entry{key: key, val: held(e.Value)}
		if err := slot(i+1, e.Right, e.RightSummary); err != nil {
			return nil, 0, err
		}
		prev = key
	}
	if li != len(links) {
		return nil, 0, malformed(c, "%d links for %d subtrees", len(links), li)
	}
	return n, cost, nil
}
