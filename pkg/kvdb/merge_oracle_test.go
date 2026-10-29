package kvdb

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"maps"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	cid "github.com/ipfs/go-cid"
	pb "github.com/taubyte/tau/pkg/kvdb/pb"
)

// Every snapshot merge of every test in this package is decided twice:
// through the tree diff, which reads only the records the trees disagree on
// and those the journal names, and through an oracle that decides every
// record either side holds. The two must write the same thing, down to the
// byte; a merge where they do not panics. A run of benchmarks goes without:
// the oracle reads every record of every merge, which is what the
// benchmarks measure the tree path for not doing.
func installOracle() {
	if flag.Lookup("test.bench").Value.String() != "" {
		return
	}
	checkSnapshot = func(ctx context.Context, store *Datastore, c cid.Cid, root *pb.Delta, s *snapState, p *snapshotMerge) error {
		if err := againstOracle(ctx, store, c, root, s, p); err != nil {
			// A panic unwinding through a test's deferred Close waits on the
			// lock this merge holds, and is never printed.
			fmt.Fprintln(os.Stderr, err)
			panic(err)
		}
		return nil
	}
}

var oracleChecks atomic.Int64

// everything is every record s holds and every one this replica holds.
func everything(ctx context.Context, store *Datastore, s *snapState) (*mergeSet, error) {
	ms := newMergeSet()
	if err := s.elems.Range(ctx, nil, nil, func(k, v []byte) error { return ms.addElem(k, v, true) }); err != nil {
		return nil, err
	}
	if err := s.pending.Range(ctx, nil, nil, func(k, v []byte) error { return ms.addPending(k, v, true) }); err != nil {
		return nil, err
	}
	err := scanStore(ctx, store.store, store.set.elemsPrefix(), func(k string, v []byte) error {
		el, err := store.set.parseElem(k, v)
		if err != nil {
			return err
		}
		return ms.addElem(elemTreeKey(el.key, el.dot), nil, false)
	})
	if err != nil {
		return nil, err
	}
	err = scanStore(ctx, store.store, store.set.pendingPrefix(), func(k string, v []byte) error {
		r, err := store.set.parsePending(k, v)
		if err != nil {
			return err
		}
		return ms.addPending(pendingTreeKey(r), nil, false)
	})
	return ms, err
}

func sameContext(a, b *causalContext) bool {
	return maps.EqualFunc(a.inc, b.inc, func(x, y *known) bool {
		return slices.Equal(x.spans, y.spans) && x.changed == y.changed && x.expires == y.expires
	})
}

// againstOracle decides the merge p decided again through everything, and
// reports how the two differ.
func againstOracle(ctx context.Context, store *Datastore, c cid.Cid, root *pb.Delta, s *snapState, p *snapshotMerge) error {
	oracleChecks.Add(1)
	ms, err := everything(ctx, store, s)
	if err != nil {
		return err
	}
	q, err := store.prepareSnapshot(ctx, c, root, s, ms)
	if err != nil {
		return fmt.Errorf("the oracle could not merge snapshot %s: %w", c, err)
	}
	// Retry marks are written by walks without store.mu, so the oracle,
	// deciding after the tree, may find one more to drop: both read them
	// through the same scan, which no enumeration feeds.
	retries := store.retryPrefix().String() + "/"
	var diffs []string
	for k, v := range p.m.t.writes {
		if strings.HasPrefix(k, retries) {
			continue
		}
		if w, ok := q.m.t.writes[k]; !ok || !bytes.Equal(v, w) || (v == nil) != (w == nil) {
			diffs = append(diffs, fmt.Sprintf("the tree writes %s=%x, the oracle %x (%t)", k, v, w, ok))
		}
	}
	for k, w := range q.m.t.writes {
		if _, ok := p.m.t.writes[k]; !ok && !strings.HasPrefix(k, retries) {
			diffs = append(diffs, fmt.Sprintf("the oracle writes %s=%x, the tree nothing", k, w))
		}
	}
	if !sameContext(p.m.cc, q.m.cc) {
		diffs = append(diffs, "the contexts differ")
	}
	if !reflect.DeepEqual(p.m.heads, q.m.heads) {
		diffs = append(diffs, "the head changes differ")
	}
	if p.m.horizon != q.m.horizon || p.m.live != q.m.live || p.m.pendingAt != q.m.pendingAt || !slices.Equal(p.m.unerased, q.m.unerased) {
		diffs = append(diffs, fmt.Sprintf("horizon %d/%d, live %d/%d, pendingAt %d/%d", p.m.horizon, q.m.horizon, p.m.live, q.m.live, p.m.pendingAt, q.m.pendingAt))
	}
	if p.differs != q.differs {
		diffs = append(diffs, fmt.Sprintf("the tree says differs %t, the oracle %t", p.differs, q.differs))
	}
	if !reflect.DeepEqual(p.walk, q.walk) {
		diffs = append(diffs, "the walks differ")
	}
	if !maps.Equal(p.residual, q.residual) {
		diffs = append(diffs, fmt.Sprintf("the residual journals differ: %v and %v", sortedKeys(p.residual), sortedKeys(q.residual)))
	}
	if len(diffs) > 0 {
		slices.Sort(diffs)
		return fmt.Errorf("merging snapshot %s: the tree and the oracle disagree:\n%s", c, joinLines(diffs))
	}
	return nil
}

func joinLines(lines []string) string {
	var b bytes.Buffer
	for _, l := range lines {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return b.String()
}

// TestTheOracleChecksEveryMerge: the check runs, and it is not vacuous: a
// merge that decides nothing is told apart from the oracle's.
func TestTheOracleChecksEveryMerge(t *testing.T) {
	replicas, dagsyncs, done := makeNReplicasSeparateStores(t, 2, noAutoCompact())
	defer done()
	a, b := replicas[0], replicas[1]
	ctx := context.Background()
	for i := range 20 {
		put(t, a, fmt.Sprintf("/k%d", i), "v")
	}
	del(t, a, "/k3")
	compact(t, a)
	from(dagsyncs, 1, 0)

	before := oracleChecks.Load()
	snap := onlyHead(t, a)
	_, root, err := (&crdtNodeGetter{b.dagService}).GetDelta(ctx, snap)
	if err != nil {
		t.Fatal(err)
	}
	f, err := b.fetchState(ctx, &crdtNodeGetter{b.dagService}, snap, root, nil)
	defer b.leaveFetch(ctx, f, true)
	if err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	s, err := b.readState(ctx, b.dagService, cid.MustParse(root.GetState()))
	if err != nil {
		t.Fatal(err)
	}
	blind, err := b.prepareSnapshot(ctx, snap, root, s, newMergeSet())
	if err != nil {
		t.Fatal(err)
	}
	if err := againstOracle(ctx, b, snap, root, s, blind); err == nil {
		t.Fatal("a merge that read no record agrees with the oracle")
	}
	if _, _, err := b.mergeSnapshot(ctx, snap, root, b.dagService, f.b.sess); err != nil {
		t.Fatal(err)
	}
	if oracleChecks.Load() <= before+1 {
		t.Fatal("the merge was not checked against the oracle")
	}
}
