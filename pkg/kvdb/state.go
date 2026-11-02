package kvdb

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"

	blocks "github.com/ipfs/go-block-format"
	cid "github.com/ipfs/go-cid"
	ipld "github.com/ipfs/go-ipld-format"
	pb "github.com/taubyte/tau/pkg/kvdb/pb"
	"github.com/taubyte/tau/pkg/kvdb/statetree"
)

// A snapshot carries a state: its live elements, its pending removals and
// its causal context, as three Merkle search trees (package statetree) named by
// one StateRoot block. The state is a pure function of those records, so
// two replicas holding the same state hold the same StateRoot CID, however
// they came to it, and a snapshot shares every subtree it did not change
// with the one before it.
//
// # State format profile 2
//
// This is the whole definition of a state's bytes. Anything that changes it
// is a new profile, with a new value of StateRoot.format.
//
// Integers in keys and values are 8-byte big-endian. A string in a key is
// escaped: each 0x00 byte is written 0x00 0xFF, and the string ends with
// 0x00 0x01. Escaping is injective and keeps unsigned byte order, so a tree
// orders its records by their fields in key order. A time is milliseconds
// since the epoch. Records are bucketed by expiry: bucket(x) is x / 3600000,
// whole hours since the epoch.
//
// The elements tree E holds one entry per live element (key, dot):
//
//	key    esc(key) || esc(replica) || counter
//	value  at || ttl || priority || value bytes
//
// The removals tree P holds one entry per pending removal of (key, dot):
//
//	key    bucket(at + ttl) || esc(key) || esc(replica) || counter
//	value  at || ttl
//
// The context tree C holds one entry per incarnation the context knows:
//
//	key    bucket(expires) || replica bytes
//	value  changed || expires || (lo || hi) for each span
//
// whose spans are the incarnation's covered counters (lo, hi], at least one,
// ascending, each with lo < hi, and no two touching (one's hi below the
// next's lo); the first is the incarnation's entry. A context's tree does
// not hold the dot of the snapshot that carries it.
//
// Every tree is a statetree profile 2 tree with the stamps summarizer: a
// record's stamp is the first 8 bytes of its value (at, or changed for the
// context), and a subtree's summary is the least stamp under it followed by
// the greatest, 16 bytes. A tree with no entry is absent.
//
// The StateRoot is a statetree Block (CIDv1, raw, sha2-256 of its bytes) whose
// data is the StateRoot message of pb/state.proto, its fields in field
// order and none at its zero value, written with statetree's rules for a Node:
// format 2, and a flag for each tree present. Its links are the roots of
// the trees present, in the order E, P, C. The empty state is a StateRoot
// with no flag set and no link.
//
// A snapshot (pb/delta.proto) names its state by the StateRoot's CID,
// state, and carries the block itself: root is its StateRoot message and
// trees the CIDs of its links, in order, each as its binary bytes. It also
// carries, in inline, the bytes of whole blocks of its trees: of the blocks
// its author wrote for it, none that a state it already held reaches,
// those breadth first from the roots of E, P and C in that order, each
// block's links in their order, a block taken only when its parent is and
// none that would take the total over 32768 bytes.
//
// A reader rebuilds the StateRoot block from root and trees and refuses the
// snapshot unless its CID is state. It takes each inline block, in order,
// as the block whose CID is CIDv1, raw, sha2-256 of its bytes, and
// refuses the snapshot when no block it took before, the StateRoot
// included, links that CID, when one is taken twice, or when the blocks
// total more than 32768 bytes. What the blocks it took link that it holds
// neither inline nor already, it fetches.
//
// A reader refuses a StateRoot with unknown fields, another format or a
// link count that is not its flag count, and a record whose key or value
// is malformed: a key that does not parse to its end, a value of the wrong
// length, a bucket that is not its record's, or spans that are not as
// above.
const (
	stateFormat = 2
	bucketWidth = 3_600_000
)

var errMalformedState = errors.New("malformed state record")

func bucket(expires uint64) uint64 { return expires / bucketWidth }

func appendEscaped(b []byte, s string) []byte {
	for i := 0; i < len(s); i++ {
		b = append(b, s[i])
		if s[i] == 0 {
			b = append(b, 0xff)
		}
	}
	return append(b, 0, 1)
}

// A string with no escaped byte aliases b, which must not change after; one
// with some is a copy.
func readEscaped(b []byte) (string, []byte, error) {
	for i := 0; i+1 < len(b); i++ {
		if b[i] != 0 {
			continue
		}
		switch b[i+1] {
		case 1:
			return stringOf(b[:i]), b[i+2:], nil
		case 0xff:
			return readUnescaped(b)
		default:
			return "", nil, errMalformedState
		}
	}
	return "", nil, errMalformedState
}

func readUnescaped(b []byte) (string, []byte, error) {
	var out []byte
	for i := 0; i < len(b); i++ {
		if b[i] != 0 {
			continue
		}
		if i+1 == len(b) {
			break
		}
		switch b[i+1] {
		case 1:
			out = append(out, b[:i]...)
			return string(out), b[i+2:], nil
		case 0xff:
			out = append(out, b[:i+1]...)
			b = b[i+2:]
			i = -1
		default:
			return "", nil, errMalformedState
		}
	}
	return "", nil, errMalformedState
}

func elemTreeKey(key string, d dot) []byte {
	return appendElemTreeKey(make([]byte, 0, len(key)+len(d.replica)+12), key, d)
}

func appendElemTreeKey(b []byte, key string, d dot) []byte {
	b = appendEscaped(b, key)
	b = appendEscaped(b, d.replica)
	return binary.BigEndian.AppendUint64(b, d.counter)
}

func parseElemTreeKey(b []byte) (string, dot, error) {
	key, rest, err := readEscaped(b)
	if err != nil {
		return "", dot{}, err
	}
	replica, rest, err := readEscaped(rest)
	if err != nil || len(rest) != 8 {
		return "", dot{}, errMalformedState
	}
	// A dot's replica is the interned one, as everywhere.
	d := dot{replica: replicas.bytes(bytesOf(replica)), counter: binary.BigEndian.Uint64(rest)}
	if d.isZero() {
		return "", dot{}, errMalformedState
	}
	return key, d, nil
}

func pendingTreeKey(r removal) []byte {
	return pendingTreeKeyIn(bucket(r.expires()), r.key, r.dot)
}

func parsePendingTreeKey(b []byte) (uint64, string, dot, error) {
	if len(b) < 8 {
		return 0, "", dot{}, errMalformedState
	}
	key, d, err := parseElemTreeKey(b[8:])
	return binary.BigEndian.Uint64(b), key, d, err
}

func contextTreeKey(replica string, k *known) []byte {
	return append(binary.BigEndian.AppendUint64(make([]byte, 0, 8+len(replica)), bucket(k.expires)), replica...)
}

func elemValue(e element) []byte { return appendElemValue(make([]byte, 0, 24+len(e.value)), e) }

func appendElemValue(b []byte, e element) []byte {
	b = binary.BigEndian.AppendUint64(b, e.at)
	b = binary.BigEndian.AppendUint64(b, e.ttl)
	b = binary.BigEndian.AppendUint64(b, e.priority)
	return append(b, e.value...)
}

func parseElemValue(key string, d dot, v []byte) (element, error) {
	if len(v) < 24 {
		return element{}, errMalformedState
	}
	e := element{
		key: key, dot: d,
		at: binary.BigEndian.Uint64(v), ttl: binary.BigEndian.Uint64(v[8:]), priority: binary.BigEndian.Uint64(v[16:]),
	}
	if len(v) > 24 {
		e.value = v[24:]
	}
	return e, nil
}

func pendingValue(r removal) []byte { return appendPendingValue(make([]byte, 0, 16), r) }

func appendPendingValue(b []byte, r removal) []byte {
	return binary.BigEndian.AppendUint64(binary.BigEndian.AppendUint64(b, r.at), r.ttl)
}

func parsePending(b []byte, v []byte) (removal, error) {
	bk, key, d, err := parsePendingTreeKey(b)
	if err != nil {
		return removal{}, err
	}
	if len(v) != 16 {
		return removal{}, errMalformedState
	}
	r := removal{key: key, dot: d, at: binary.BigEndian.Uint64(v), ttl: binary.BigEndian.Uint64(v[8:])}
	if r.at+r.ttl < r.at || bucket(r.expires()) != bk {
		return removal{}, errMalformedState
	}
	return r, nil
}

func contextValue(k *known) []byte {
	b := make([]byte, 0, 16+16*len(k.spans))
	b = binary.BigEndian.AppendUint64(b, k.changed)
	b = binary.BigEndian.AppendUint64(b, k.expires)
	for _, s := range k.spans {
		b = binary.BigEndian.AppendUint64(binary.BigEndian.AppendUint64(b, s.lo), s.hi)
	}
	return b
}

func parseContextEntry(key, v []byte) (string, *known, error) {
	if len(key) < 8 || len(v) < 32 || len(v)%16 != 0 {
		return "", nil, errMalformedState
	}
	k := &known{changed: binary.BigEndian.Uint64(v), expires: binary.BigEndian.Uint64(v[8:])}
	if bucket(k.expires) != binary.BigEndian.Uint64(key) {
		return "", nil, errMalformedState
	}
	for i := 16; i < len(v); i += 16 {
		s := span{lo: binary.BigEndian.Uint64(v[i:]), hi: binary.BigEndian.Uint64(v[i+8:])}
		if s.lo >= s.hi || (len(k.spans) > 0 && k.spans[len(k.spans)-1].hi >= s.lo) {
			return "", nil, errMalformedState
		}
		k.spans = append(k.spans, s)
	}
	return string(key[8:]), k, nil
}

// stamps summarizes a subtree by the least and greatest stamp under it.
type stamps struct{}

func stampOf(v []byte) uint64 {
	if len(v) < 8 {
		return 0
	}
	return binary.BigEndian.Uint64(v)
}

func (stamps) Leaf(dst, _, v []byte) []byte {
	s := stampOf(v)
	return binary.BigEndian.AppendUint64(binary.BigEndian.AppendUint64(dst, s), s)
}

func (stamps) Merge(dst, a, b []byte) []byte {
	lo, hi := summaryRange(a)
	blo, bhi := summaryRange(b)
	return binary.BigEndian.AppendUint64(binary.BigEndian.AppendUint64(dst, min(lo, blo)), max(hi, bhi))
}

func summaryRange(s []byte) (uint64, uint64) {
	if len(s) != 16 {
		return 0, 0
	}
	return binary.BigEndian.Uint64(s), binary.BigEndian.Uint64(s[8:])
}

func stateTree(dags ipld.DAGService, root cid.Cid) *statetree.Tree {
	return statetree.Load(dags, root, statetree.WithSummarizer(stamps{}))
}

const treeCacheBytes = 256 << 10

type treeCache = statetree.Cache

func newTreeCache(opts *Options) *treeCache {
	if opts.treeCache > 0 {
		return statetree.NewCache(opts.treeCache)
	}
	return statetree.NewCache(treeCacheBytes)
}

func (store *Datastore) tree(dags ipld.DAGService, root cid.Cid) *statetree.Tree {
	return statetree.Load(dags, root, statetree.WithSummarizer(stamps{}), statetree.WithCache(store.trees))
}

// cid.Undef for an empty tree.
type stateRoot struct {
	elems, pending, ctx cid.Cid
}

func (s stateRoot) message(msg *pb.StateRoot) (present [3]cid.Cid, n int) {
	msg.Format, msg.Elements, msg.Removals, msg.Context = stateFormat, s.elems.Defined(), s.pending.Defined(), s.ctx.Defined()
	for _, c := range [...]cid.Cid{s.elems, s.pending, s.ctx} {
		if c.Defined() {
			present[n] = c
			n++
		}
	}
	return present, n
}

func (s stateRoot) node() (*statetree.Block, error) {
	var msg pb.StateRoot
	present, n := s.message(&msg)
	data, err := msg.MarshalVT()
	if err != nil {
		return nil, err
	}
	return statetree.NewBlock(data, present[:n])
}

func decodeStateRoot(nd ipld.Node) (stateRoot, error) {
	var buf [3]cid.Cid
	data, links, err := statetree.DecodeBlock(nd, buf[:0])
	if err != nil {
		return stateRoot{}, err
	}
	var msg pb.StateRoot
	if err := msg.UnmarshalVT(data); err != nil {
		return stateRoot{}, fmt.Errorf("state root %s: %w", nd.Cid(), err)
	}
	s, err := stateRootOf(&msg, len(links), func(i int) (cid.Cid, error) { return links[i], nil })
	if err != nil {
		return stateRoot{}, fmt.Errorf("state root %s: %w", nd.Cid(), err)
	}
	return s, nil
}

func stateRootOf(msg *pb.StateRoot, n int, link func(int) (cid.Cid, error)) (stateRoot, error) {
	if msg.Format != stateFormat || len(msg.ProtoReflect().GetUnknown()) > 0 {
		return stateRoot{}, errMalformedState
	}
	var s stateRoot
	i := 0
	type stateSlot struct {
		present bool
		c       *cid.Cid
	}
	for _, slot := range [...]stateSlot{{msg.Elements, &s.elems}, {msg.Removals, &s.pending}, {msg.Context, &s.ctx}} {
		if !slot.present {
			continue
		}
		if i == n {
			return stateRoot{}, errMalformedState
		}
		c, err := link(i)
		if err != nil {
			return stateRoot{}, err
		}
		*slot.c = c
		i++
	}
	if i != n {
		return stateRoot{}, errMalformedState
	}
	return s, nil
}

func loadStateRoot(ctx context.Context, ng ipld.NodeGetter, c cid.Cid) (stateRoot, error) {
	nd, err := ng.Get(ctx, c)
	if err != nil {
		return stateRoot{}, err
	}
	return decodeStateRoot(nd)
}

func contextTree(ctx context.Context, dags ipld.DAGService, cc *causalContext) (*statetree.Tree, error) {
	type kv struct{ k, v []byte }
	entries := make([]kv, 0, len(cc.inc))
	for r, k := range cc.inc {
		entries = append(entries, kv{contextTreeKey(r, k), contextValue(k)})
	}
	slices.SortFunc(entries, func(a, b kv) int { return bytes.Compare(a.k, b.k) })
	return statetree.Build(ctx, dags, func(yield func([]byte, []byte) bool) {
		for _, e := range entries {
			if !yield(e.k, e.v) {
				return
			}
		}
	}, statetree.WithSummarizer(stamps{}))
}

func readContext(ctx context.Context, t *statetree.Tree) (*causalContext, error) {
	cc := newCausalContext()
	err := t.Range(ctx, nil, nil, func(key, v []byte) error {
		r, k, err := parseContextEntry(key, v)
		if err != nil {
			return err
		}
		if cc.inc[r] != nil {
			return errMalformedState
		}
		cc.inc[r] = k
		return nil
	})
	return cc, err
}

func treeStamps(ctx context.Context, t *statetree.Tree) (uint64, uint64, bool, error) {
	s, err := t.Summary(ctx)
	if err != nil || s == nil {
		return 0, 0, false, err
	}
	lo, hi := summaryRange(s)
	return lo, hi, true, nil
}

// An overlay holds in memory the blocks a run of tree changes writes: each
// change rewrites its path, and only the nodes the final trees reach are worth
// writing.
type overlay struct {
	ipld.DAGService
	added map[cid.Cid]ipld.Node
}

func newOverlay(under ipld.DAGService) *overlay {
	return &overlay{DAGService: under, added: map[cid.Cid]ipld.Node{}}
}

func (o *overlay) Get(ctx context.Context, c cid.Cid) (ipld.Node, error) {
	if nd, ok := o.added[c]; ok {
		return nd, nil
	}
	return o.DAGService.Get(ctx, c)
}

func (o *overlay) ReadBlock(ctx context.Context, c cid.Cid) (blocks.Block, error) {
	if nd, ok := o.added[c]; ok {
		return nd, nil
	}
	return readBlock(ctx, o.DAGService, c)
}

func (o *overlay) Add(_ context.Context, nd ipld.Node) error {
	o.added[nd.Cid()] = nd
	return nil
}

func (o *overlay) AddMany(_ context.Context, nds []ipld.Node) error {
	for _, nd := range nds {
		o.added[nd.Cid()] = nd
	}
	return nil
}

// fresh is the blocks under roots the overlay holds and owned does not name:
// one it does not hold is already stored, an owned one held with all below it.
func (o *overlay) fresh(roots []cid.Cid, owned func(cid.Cid) (bool, error)) ([]ipld.Node, error) {
	var out []ipld.Node
	seen := map[cid.Cid]struct{}{}
	stack := append([]cid.Cid(nil), roots...)
	for len(stack) > 0 {
		c := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if _, ok := seen[c]; ok || !c.Defined() {
			continue
		}
		seen[c] = struct{}{}
		nd, ok := o.added[c]
		if !ok {
			continue
		}
		if own, err := owned(c); err != nil || own {
			if err != nil {
				return nil, err
			}
			continue
		}
		out = append(out, nd)
		var err error
		if stack, err = blockLinks(nd, stack); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func pendingTreeKeyIn(b uint64, key string, d dot) []byte {
	return appendPendingTreeKey(make([]byte, 0, 8+len(key)+len(d.replica)+12), b, key, d)
}

func appendPendingTreeKey(k []byte, b uint64, key string, d dot) []byte {
	return appendElemTreeKey(binary.BigEndian.AppendUint64(k, b), key, d)
}
