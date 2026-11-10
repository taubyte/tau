package kvdb

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"testing"

	dag "github.com/ipfs/boxo/ipld/merkledag"
	mdutils "github.com/ipfs/boxo/ipld/merkledag/test"
	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	ipld "github.com/ipfs/go-ipld-format"
	pb "github.com/taubyte/tau/pkg/kvdb/pb"
	"github.com/taubyte/tau/pkg/kvdb/statetree"
	"google.golang.org/protobuf/proto"
)

func buildTree(t testing.TB, dags ipld.DAGService, entries map[string][]byte) cid.Cid {
	t.Helper()
	keys := sortedKeys(entries)
	tr, err := statetree.Build(context.Background(), dags, func(yield func([]byte, []byte) bool) {
		for _, k := range keys {
			if !yield([]byte(k), entries[k]) {
				return
			}
		}
	}, statetree.WithSummarizer(stamps{}))
	if err != nil {
		t.Fatal(err)
	}
	return tr.Root()
}

func putState(t testing.TB, dags ipld.DAGService, elems, pending, cc map[string][]byte) cid.Cid {
	t.Helper()
	nd, err := stateRoot{elems: buildTree(t, dags, elems), pending: buildTree(t, dags, pending), ctx: buildTree(t, dags, cc)}.node()
	if err != nil {
		t.Fatal(err)
	}
	if err := dags.Add(context.Background(), nd); err != nil {
		t.Fatal(err)
	}
	return nd.Cid()
}

func stateBlocks(t testing.TB, r *Datastore, snap cid.Cid) []cid.Cid {
	t.Helper()
	ctx := context.Background()
	_, root, err := (&crdtNodeGetter{r.dagService}).GetDelta(ctx, snap)
	if err != nil {
		t.Fatal(err)
	}
	stc, err := cid.Cast(root.GetState())
	if err != nil {
		t.Fatal(err)
	}
	out := []cid.Cid{stc}
	for i := 0; i < len(out); i++ {
		nd, err := r.dagService.Get(ctx, out[i])
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range blockLinksOf(t, nd) {
			if !slices.Contains(out, l) {
				out = append(out, l)
			}
		}
	}
	return out
}

func owned(t testing.TB, r *Datastore) map[cid.Cid]bool {
	t.Helper()
	r.gcMu.Lock()
	defer r.gcMu.Unlock()
	out := map[cid.Cid]bool{}
	for k := range r.gc.members {
		out[cidOf(k)] = true
	}
	return out
}

func (store *Datastore) reaches(c cid.Cid) bool {
	store.gcMu.Lock()
	defer store.gcMu.Unlock()
	return store.gc.member(keyOf(c))
}

func heldBesidesState(t testing.TB, r *Datastore, d *fetchThroughDAGSvc) int {
	t.Helper()
	return d.blockCount(t) - len(owned(t, r))
}

func TestEscapingKeepsOrderAndParses(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	alphabet := []byte{0, 1, 2, 0xfe, 0xff, 'a'}
	str := func() string {
		b := make([]byte, rng.Intn(4))
		for i := range b {
			b[i] = alphabet[rng.Intn(len(alphabet))]
		}
		return string(b)
	}
	type id struct {
		key string
		d   dot
	}
	cmp := func(a, b id) int {
		if c := bytes.Compare([]byte(a.key), []byte(b.key)); c != 0 {
			return c
		}
		if c := bytes.Compare([]byte(a.d.replica), []byte(b.d.replica)); c != 0 {
			return c
		}
		return int(a.d.counter) - int(b.d.counter)
	}
	for range 5000 {
		a := id{str(), dot{str(), uint64(rng.Intn(3) + 1)}}
		b := id{str(), dot{str(), uint64(rng.Intn(3) + 1)}}
		ka, kb := elemTreeKey(a.key, a.d), elemTreeKey(b.key, b.d)
		if got, want := bytes.Compare(ka, kb), cmp(a, b); (got < 0) != (want < 0) || (got == 0) != (want == 0) {
			t.Fatalf("%+v vs %+v: keys compare %d, identities %d", a, b, got, want)
		}
		key, d, err := new(internTable).parseElemTreeKey(ka)
		if err != nil || key != a.key || d != a.d {
			t.Fatalf("%+v parsed as %q %v (%v)", a, key, d, err)
		}
	}
	for _, bad := range [][]byte{nil, {0}, {0, 2}, {'a', 0, 1}, append(elemTreeKey("k", dot{"r", 1}), 9), elemTreeKey("k", dot{"r", 0})} {
		if _, _, err := new(internTable).parseElemTreeKey(bad); err == nil {
			t.Errorf("%x parsed", bad)
		}
	}
}

func TestStateRecordsRoundTrip(t *testing.T) {
	el := element{key: "k", dot: dot{"r", 3}, priority: 7, at: 100, ttl: 50, value: []byte("v")}
	got, err := parseElemValue(el.key, el.dot, elemValue(el))
	if err != nil || !bytes.Equal(got.value, el.value) || got.priority != 7 || got.at != 100 || got.ttl != 50 {
		t.Fatalf("element came back as %+v (%v)", got, err)
	}
	if _, err := parseElemValue("k", el.dot, make([]byte, 23)); err == nil {
		t.Error("a short element value parsed")
	}
	r := removal{key: "k", dot: dot{"r", 3}, at: bucketWidth - 10, ttl: 20}
	pr, err := new(internTable).parsePending(pendingTreeKey(r), pendingValue(r))
	if err != nil || pr != r {
		t.Fatalf("removal came back as %+v (%v)", pr, err)
	}
	if _, err := new(internTable).parsePending(pendingTreeKeyIn(0, r.key, r.dot), pendingValue(r)); err == nil {
		t.Error("a removal under another bucket parsed")
	}
	if _, err := new(internTable).parsePending(pendingTreeKey(r), pendingValue(r)[:15]); err == nil {
		t.Error("a short removal value parsed")
	}
	if _, err := new(internTable).parsePending([]byte{1}, nil); err == nil {
		t.Error("a short removal key parsed")
	}
	if _, _, _, err := new(internTable).parsePendingTreeKey([]byte{1, 2}); err == nil {
		t.Error("a short pending key parsed")
	}
	k := &known{spans: []span{{0, 4}, {6, 9}}, changed: 5, expires: 3 * bucketWidth}
	rep, kk, err := parseContextEntry(contextTreeKey("r", k), contextValue(k))
	if err != nil || rep != "r" || !slices.Equal(kk.spans, k.spans) || kk.changed != 5 || kk.expires != k.expires {
		t.Fatalf("context entry came back as %q %+v (%v)", rep, kk, err)
	}
	for name, bad := range map[string]*known{
		"no span":       {expires: 1},
		"empty span":    {spans: []span{{4, 4}}},
		"touching":      {spans: []span{{0, 4}, {4, 6}}},
		"out of order":  {spans: []span{{6, 9}, {0, 4}}},
		"overlapping":   {spans: []span{{0, 5}, {3, 9}}},
		"other bucket?": nil,
	} {
		if bad == nil {
			if _, _, err := parseContextEntry(contextTreeKey("r", &known{expires: 0}), contextValue(k)); err == nil {
				t.Errorf("%s: a context entry under another bucket parsed", name)
			}
			continue
		}
		if _, _, err := parseContextEntry(contextTreeKey("r", bad), contextValue(bad)); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
}

func TestStateRootDecodingRefusesWhatItDoesNotWrite(t *testing.T) {
	dags := mdutils.Mock()
	ctx := context.Background()
	leaf := dag.NodeWithData([]byte("x"))
	for name, sr := range map[string]stateRoot{
		"empty": {},
		"all":   {elems: leaf.Cid(), pending: leaf.Cid(), ctx: leaf.Cid()},
		"some":  {pending: leaf.Cid()},
	} {
		nd, err := sr.node()
		if err != nil {
			t.Fatal(err)
		}
		got, err := decodeStateRoot(nd)
		if err != nil || got != sr {
			t.Errorf("%s: came back as %+v (%v)", name, got, err)
		}
	}
	block := func(data []byte, links int) ipld.Node {
		var cids []cid.Cid
		for range links {
			cids = append(cids, leaf.Cid())
		}
		nd, err := statetree.NewBlock(data, cids)
		if err != nil {
			t.Fatal(err)
		}
		return nd
	}
	bad := func(msg *pb.StateRoot, links int) ipld.Node {
		data, err := proto.Marshal(msg)
		if err != nil {
			t.Fatal(err)
		}
		return block(data, links)
	}
	known, err := proto.Marshal(&pb.StateRoot{Format: stateFormat})
	if err != nil {
		t.Fatal(err)
	}
	unknown := block(append(known, 0x78, 1), 0)
	for name, nd := range map[string]ipld.Node{
		"format":        bad(&pb.StateRoot{Format: stateFormat + 1}, 0),
		"missing link":  bad(&pb.StateRoot{Format: stateFormat, Elements: true}, 0),
		"extra link":    bad(&pb.StateRoot{Format: stateFormat}, 1),
		"unknown field": unknown,
		"not protobuf":  block([]byte{0xff}, 0),
		"not a block":   dag.NewRawNode([]byte{1}),
	} {
		if _, err := decodeStateRoot(nd); err == nil {
			t.Errorf("%s: decoded", name)
		}
	}
	if _, err := loadStateRoot(ctx, dags, leaf.Cid()); err == nil {
		t.Error("a block nobody holds loaded")
	}
}

func TestStampsSummarizeTheLeastAndGreatest(t *testing.T) {
	dags := mdutils.Mock()
	ctx := context.Background()
	entries := map[string][]byte{}
	lo, hi := ^uint64(0), uint64(0)
	rng := rand.New(rand.NewSource(2))
	for i := range 300 {
		at := uint64(rng.Intn(1 << 30))
		lo, hi = min(lo, at), max(hi, at)
		entries[fmt.Sprintf("k%03d", i)] = binary.BigEndian.AppendUint64(nil, at)
	}
	gl, gh, any, err := treeStamps(ctx, stateTree(dags, buildTree(t, dags, entries)))
	if err != nil || !any || gl != lo || gh != hi {
		t.Fatalf("stamps %d..%d (%t, %v), want %d..%d", gl, gh, any, err, lo, hi)
	}
	if _, _, any, err := treeStamps(ctx, stateTree(dags, cid.Undef)); err != nil || any {
		t.Fatalf("the empty tree has stamps (%v)", err)
	}
	if got := (stamps{}).Leaf(nil, nil, []byte{1}); !bytes.Equal(got, make([]byte, 16)) {
		t.Errorf("a short value's stamp is %x", got)
	}
	if lo, hi := summaryRange([]byte{1}); lo != 0 || hi != 0 {
		t.Errorf("a short summary reads %d..%d", lo, hi)
	}
}

func TestATreeIsTheSameWhateverTheOrderOfItsChanges(t *testing.T) {
	dags := mdutils.Mock()
	ctx := context.Background()
	final := map[string][]byte{}
	for i := range 500 {
		final[string(elemTreeKey(fmt.Sprintf("/key/%d", i), dot{"r", uint64(i%7 + 1)}))] = elemValue(element{at: uint64(i)})
	}
	want := buildTree(t, dags, final)
	for seed := range int64(3) {
		rng := rand.New(rand.NewSource(seed))
		tr := stateTree(dags, cid.Undef)
		keys := sortedKeys(final)
		rng.Shuffle(len(keys), func(i, j int) { keys[i], keys[j] = keys[j], keys[i] })
		var err error
		for i, k := range keys {
			if i%5 == 0 {
				if tr, err = tr.Put(ctx, []byte(k+"x"), []byte("gone")); err != nil {
					t.Fatal(err)
				}
			}
			if tr, err = tr.Put(ctx, []byte(k), final[k]); err != nil {
				t.Fatal(err)
			}
		}
		for i, k := range keys {
			if i%5 == 0 {
				if tr, err = tr.Delete(ctx, []byte(k+"x")); err != nil {
					t.Fatal(err)
				}
			}
		}
		if tr.Root() != want {
			t.Fatalf("order %d gives root %s, building gives %s", seed, tr.Root(), want)
		}
	}
}

func TestBaseEncoding(t *testing.T) {
	leaf := dag.NodeWithData([]byte("x"))
	for _, b := range []stateBase{{}, {elems: leaf.Cid()}, {pending: leaf.Cid()}, {elems: leaf.Cid(), pending: leaf.Cid()}} {
		got, err := decodeBase(encodeBase(b))
		if err != nil || got != b {
			t.Errorf("%+v came back as %+v (%v)", b, got, err)
		}
	}
	for _, bad := range [][]byte{nil, {5}, {1, 1, 0}, append(encodeBase(stateBase{}), 0)} {
		if _, err := decodeBase(bad); err == nil {
			t.Errorf("%x decoded", bad)
		}
	}
}

func stateOf(t testing.TB, r *Datastore) cid.Cid {
	t.Helper()
	_, root, err := (&crdtNodeGetter{r.dagService}).GetDelta(context.Background(), onlyHead(t, r))
	if err != nil {
		t.Fatal(err)
	}
	c, err := cid.Cast(root.GetState())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestEqualStatesHaveEqualStateRoots(t *testing.T) {
	replicas, dagsyncs, done := makeNReplicasSeparateStores(t, 2, noAutoCompact())
	defer done()
	a, b := replicas[0], replicas[1]
	from(dagsyncs, 0, 1)
	from(dagsyncs, 1, 0)
	for i := range 40 {
		put(t, a, fmt.Sprintf("/a/%d", i), "x")
		put(t, b, fmt.Sprintf("/b/%d", 39-i), "y")
		if i%7 == 0 {
			syncReplicaHeads(t, a, b)
		}
	}
	del(t, a, "/a/3")
	del(t, b, "/a/5")
	compact(t, b)
	syncReplicaHeads(t, a, b)
	syncReplicaHeads(t, b, a)
	syncReplicaHeads(t, a, b)
	assertSameState(t, []string{"a", "b"}, a, b)
	compact(t, a)
	compact(t, b)
	if sa, sb := stateOf(t, a), stateOf(t, b); sa != sb {
		t.Fatalf("one state, two roots: %s and %s", sa, sb)
	}
	if onlyHead(t, a) == onlyHead(t, b) {
		t.Fatal("test setup: the snapshots are one block")
	}
}

func TestTheJournalMarksEveryRecordWrite(t *testing.T) {
	ctx := context.Background()
	inner := ds.NewMapDatastore()
	ns := ds.NewKey("j")
	s := newSet(nil, ns.ChildString(setNs), new(internTable))
	j := newJournal(inner, ns, s)
	store := &journaled{Datastore: inner, j: j}
	d := dot{"r", 4}
	r := removal{key: "/k", dot: d, at: 2 * bucketWidth, ttl: 10}
	if err := store.Put(ctx, s.elemKey("/k", d), encodeMarker(element{value: []byte("v")})); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, s.pendingKey("/k", d), encodeStamps(r.at, r.ttl)); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, s.pendingKey("/k", d), encodeStamps(r.at, bucketWidth)); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, s.elemKey("/k", d)); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, ns.ChildString("other"), nil); err != nil {
		t.Fatal(err)
	}
	sb := stateBuild{replicas: new(internTable)}
	parseElemMark := func(k string) (string, dot, error) {
		return sb.elemMark([]byte(strings.TrimPrefix(k, j.jElems)))
	}
	parsePendingMark := func(k string) (uint64, string, dot, error) {
		return sb.pendingMark([]byte(strings.TrimPrefix(k, j.jPending)))
	}
	var elems, pending []string
	if err := scanStore(ctx, inner, j.elemsPrefix(), func(k string, _ []byte) error {
		key, md, err := parseElemMark(k)
		elems = append(elems, fmt.Sprintf("%s@%d", key, md.counter))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := scanStore(ctx, inner, j.pendingPrefix(), func(k string, _ []byte) error {
		b, key, md, err := parsePendingMark(k)
		pending = append(pending, fmt.Sprintf("%d:%s@%d", b, key, md.counter))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	slices.Sort(pending)
	if !slices.Equal(elems, []string{"/k@4"}) || !slices.Equal(pending, []string{"2:/k@4", "3:/k@4"}) {
		t.Fatalf("marks %v and %v", elems, pending)
	}
	if err := store.Put(ctx, s.pendingKey("/k", d), []byte{0xff}); err == nil {
		t.Error("a removal with no stamps was marked")
	}
	for _, bad := range []string{j.jElems + "nokey", j.jElems + "kMFRA/x"} {
		if _, _, err := parseElemMark(bad); err == nil {
			t.Errorf("%s parsed", bad)
		}
	}
	for _, bad := range []string{j.jPending + "1/2", j.jPending + "x/" + d.segment() + "/" + keySegment("/k"), j.jPending + "1/x/" + keySegment("/k")} {
		if _, _, _, err := parsePendingMark(bad); err == nil {
			t.Errorf("%s parsed", bad)
		}
	}
}
