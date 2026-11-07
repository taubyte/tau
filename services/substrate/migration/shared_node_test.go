package migration

import (
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	ds "github.com/ipfs/go-datastore"
	query "github.com/ipfs/go-datastore/query"
	"github.com/taubyte/tau/p2p/peer"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	mh "github.com/taubyte/tau/utils/multihash"
)

// The migration runs only when substrate is alone on its node (new.go skips
// it otherwise). These tests prove that even there it deletes only what the
// scrubbed namespaces reach: blocks nothing names, other files' chunks and
// non-instance namespaces survive.

func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

// readFile reads a file back from the node's local store, bounded so a swept
// block surfaces as an error instead of a hung network fetch.
func readFile(t *testing.T, node peer.Node, cid string) ([]byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	f, err := node.GetFile(ctx, cid)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

func mustEntries(t *testing.T, m *Migrator, hash string) map[string][]byte {
	t.Helper()
	entries, err := Entries(t.Context(), m.node.Store(), hash)
	if err != nil {
		t.Fatalf("reading namespace %s failed: %v", hash, err)
	}
	return entries
}

func TestMigrate_KeepsUnreferencedBlocks(t *testing.T) {
	m, h, tns := newTestMigrator(t)
	ctx := t.Context()

	// A cached artifact and a log: blocks no kvdb references.
	artifact := randomBytes(t, 1<<20)
	artifactCid, err := m.node.AddFile(bytes.NewReader(artifact))
	if err != nil {
		t.Fatal(err)
	}
	logBytes := []byte("build log: ok\n")
	logCid, err := m.node.AddFile(bytes.NewReader(logBytes))
	if err != nil {
		t.Fatal(err)
	}

	// A non-instance namespace.
	seedLegacy(t, m.node, "auth", map[string][]byte{"/github/repo/1": []byte("secret")})

	// One genuine legacy substrate database.
	legacy := mh.Hash("p1" + "" + "users")
	seedLegacy(t, m.node, legacy, map[string][]byte{"k": []byte("v")})
	tns.indexKeys = []string{"/projects/p1/x"}
	tns.dbs["p1/"] = map[string]*structureSpec.Database{"cfg": {Match: "users"}}

	report := m.Migrate(ctx)

	if got, err := readFile(t, m.node, artifactCid); err != nil || !bytes.Equal(got, artifact) {
		t.Fatalf("artifact lost (swept %d blocks): read %d bytes, err %v", report.SweptBlocks, len(got), err)
	}
	if got, err := readFile(t, m.node, logCid); err != nil || !bytes.Equal(got, logBytes) {
		t.Fatalf("log lost (swept %d blocks): %q, err %v", report.SweptBlocks, got, err)
	}
	if v := mustEntries(t, m, "auth")["/github/repo/1"]; string(v) != "secret" {
		t.Fatalf("auth database lost its key: %q", v)
	}
	rep := report.Instances[legacy]
	if rep == nil || !rep.Scrubbed {
		t.Fatalf("legacy database not migrated: %+v (%s)", rep, report.Summary())
	}
	if string(h.instance(legacy).data["/k"]) != "v" {
		t.Fatal("legacy database not replayed")
	}
	if n := countPrefix(t, m, crdtPrefix+legacy+"/"); n != 0 {
		t.Fatalf("legacy namespace not scrubbed: %d keys", n)
	}
}

func TestMigrate_ServiceDBsAreForeignAndDrainEnds(t *testing.T) {
	restore := DrainInterval
	DrainInterval = 20 * time.Millisecond
	defer func() { DrainInterval = restore }()

	m, _, tns := newTestMigrator(t)
	t.Cleanup(m.Close)

	seedLegacy(t, m.node, "auth", map[string][]byte{"a": []byte("1")})
	seedLegacy(t, m.node, "patrick", map[string][]byte{"p": []byte("1")})
	legacy := mh.Hash("p1" + "" + "users")
	seedLegacy(t, m.node, legacy, map[string][]byte{"k": []byte("v")})
	tns.indexKeys = []string{"/projects/p1/x"}
	tns.dbs["p1/"] = map[string]*structureSpec.Database{"cfg": {Match: "users"}}

	report := m.Boot()
	if rep := report.Instances[legacy]; rep == nil || !rep.Scrubbed {
		t.Fatalf("legacy database not migrated: %s", report.Summary())
	}
	if report.RemainingCount() != 0 {
		t.Errorf("service databases must not count as remaining: %d remaining (unresolved %v)", report.RemainingCount(), report.Unresolved)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		m.drainMu.Lock()
		on := m.drainOn
		m.drainMu.Unlock()
		if !on {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("drain still running after 2s (100 intervals): it never ends on a shared node")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if string(mustEntries(t, m, "auth")["/a"]) != "1" || string(mustEntries(t, m, "patrick")["/p"]) != "1" {
		t.Fatal("service databases touched")
	}
}

func TestMigrate_SweepOnlyCandidates(t *testing.T) {
	m, _, tns := newTestMigrator(t)
	ctx := t.Context()

	// Files share their first 256 KiB chunk (the default chunker size).
	const chunk = 256 << 10
	shared, onlyLegacy, onlyOther := randomBytes(t, chunk), randomBytes(t, chunk), randomBytes(t, chunk)
	legacyFile := append(append([]byte{}, shared...), onlyLegacy...)
	otherFile := append(append([]byte{}, shared...), onlyOther...)

	legacyCid, err := m.node.AddFile(bytes.NewReader(legacyFile))
	if err != nil {
		t.Fatal(err)
	}
	otherCid, err := m.node.AddFile(bytes.NewReader(otherFile))
	if err != nil {
		t.Fatal(err)
	}

	hash := mh.Hash("p1" + "" + "files")
	seedLegacy(t, m.node, hash, map[string][]byte{"file/data/1": []byte(legacyCid), "v/data": []byte("1")})
	tns.indexKeys = []string{"/projects/p1/x"}
	tns.storages["p1/"] = map[string]*structureSpec.Storage{"cfg": {Match: "files"}}

	// The other file belongs to a namespace nothing resolves yet: it stays.
	other := mh.Hash("p1" + "" + "mystery")
	seedLegacy(t, m.node, other, map[string][]byte{"file/data/1": []byte(otherCid)})

	report := m.Migrate(ctx)
	if rep := report.Instances[hash]; rep == nil || !rep.Scrubbed {
		t.Fatalf("legacy storage not migrated: %+v (%s)", rep, report.Summary())
	}
	if report.SweptBlocks == 0 {
		t.Fatal("the migrated file's own blocks were not swept")
	}
	if got, err := readFile(t, m.node, otherCid); err != nil || !bytes.Equal(got, otherFile) {
		t.Fatalf("file sharing a chunk lost (swept %d blocks): read %d bytes, err %v", report.SweptBlocks, len(got), err)
	}
}

// countingStore counts /crdt enumerations the migrator makes.
type countingStore struct {
	ds.Batching
	crdtQueries atomic.Int32
}

func (c *countingStore) Query(ctx context.Context, q query.Query) (query.Results, error) {
	if strings.HasPrefix(q.Prefix, "/crdt") {
		c.crdtQueries.Add(1)
	}
	return c.Batching.Query(ctx, q)
}

type countingNode struct {
	peer.Node
	store *countingStore
}

func (n *countingNode) Store() ds.Batching { return n.store }

// 2.2: after a clean pass the done marker is written and a later Boot does
// not enumerate /crdt at all.
// Assumes the marker key "/substrate/migration/done" in node.Store().
func TestBoot_DoneMarkerSkips(t *testing.T) {
	base := peer.Mock(t.Context())
	node := &countingNode{Node: base, store: &countingStore{Batching: base.Store()}}
	h := newFakeHoarder()
	tns := &fakeTns{
		indexKeys: []string{"/projects/p1/x"},
		dbs:       map[string]map[string]*structureSpec.Database{"p1/": {"cfg": {Match: "users"}}},
		storages:  map[string]map[string]*structureSpec.Storage{},
	}
	hash := mh.Hash("p1" + "" + "users")
	seedLegacy(t, base, hash, map[string][]byte{"k": []byte("v")})

	first := New(t.Context(), node, h, tns)
	t.Cleanup(first.Close)
	if rep := first.Boot(); rep.RemainingCount() != 0 || rep.Err != "" {
		t.Fatalf("first boot not clean: %s", rep.Summary())
	}
	if ok, err := base.Store().Has(t.Context(), ds.NewKey("/substrate/migration/done")); err != nil || !ok {
		t.Fatalf("done marker not written after a clean pass (ok=%v err=%v)", ok, err)
	}

	node.store.crdtQueries.Store(0)
	second := New(t.Context(), node, h, tns)
	t.Cleanup(second.Close)
	second.Boot()
	if n := node.store.crdtQueries.Load(); n != 0 {
		t.Fatalf("boot after the done marker queried /crdt %d time(s)", n)
	}
}
