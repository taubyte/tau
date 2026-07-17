package migration

import (
	"bytes"
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	peerCore "github.com/libp2p/go-libp2p/core/peer"
	coreKvdb "github.com/taubyte/tau/core/kvdb"
	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
	tnsIface "github.com/taubyte/tau/core/services/tns"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/kvdb"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	mh "github.com/taubyte/tau/utils/multihash"
)

// --- fakes ------------------------------------------------------------------

// fakeNx is an in-memory NxKVDB standing in for the hoarder-hosted instance.
type fakeNx struct {
	coreKvdb.KVDB // panics on anything not overridden
	mu            sync.Mutex
	data          map[string][]byte
	failPut       bool
	dropOnVerify  string // key Get pretends is missing
}

func newFakeNx() *fakeNx { return &fakeNx{data: make(map[string][]byte)} }

func (f *fakeNx) PutNx(_ context.Context, key string, v []byte) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failPut {
		return false, errors.New("injected put failure")
	}
	if _, ok := f.data[key]; ok {
		return true, nil
	}
	f.data[key] = append([]byte{}, v...)
	return false, nil
}

func (f *fakeNx) Get(_ context.Context, key string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if key == f.dropOnVerify {
		return nil, errors.New("key not found")
	}
	v, ok := f.data[key]
	if !ok {
		return nil, errors.New("key not found")
	}
	return v, nil
}

func (f *fakeNx) Close() {}

// fakeHoarder implements the hoarder client over in-memory state.
type fakeHoarder struct {
	hoarderIface.Client
	mu       sync.Mutex
	kvs      map[string]*fakeNx // instance hash → store
	stashed  map[string][]byte  // cid → bytes
	claims   map[string]int
	target   int
	metas    map[string]hoarderIface.InstanceInfo
	metasErr error
}

func newFakeHoarder() *fakeHoarder {
	return &fakeHoarder{
		kvs:     make(map[string]*fakeNx),
		stashed: make(map[string][]byte),
		claims:  make(map[string]int),
		target:  1,
		metas:   make(map[string]hoarderIface.InstanceInfo),
	}
}

func (f *fakeHoarder) instance(hash string) *fakeNx {
	f.mu.Lock()
	defer f.mu.Unlock()
	if kv, ok := f.kvs[hash]; ok {
		return kv
	}
	kv := newFakeNx()
	f.kvs[hash] = kv
	return kv
}

func (f *fakeHoarder) KVDB(kind hoarderIface.ResourceKind, project, app, match, _ string) (coreKvdb.KVDB, error) {
	m := match
	if kind == hoarderIface.Global {
		m = "global"
	}
	return f.instance(mh.Hash(project + app + m)), nil
}

func (f *fakeHoarder) Stash(cid string, data io.Reader, _ ...hoarderIface.StashOption) error {
	b, err := io.ReadAll(data)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stashed[cid] = b
	if f.claims[cid] == 0 {
		f.claims[cid] = 1
	}
	return nil
}

func (f *fakeHoarder) StashStatus(cids ...string) (map[string]int, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]int, len(cids))
	for _, c := range cids {
		out[c] = f.claims[c]
	}
	return out, f.target, nil
}

func (f *fakeHoarder) Metas(hashes ...string) ([]hoarderIface.InstanceInfo, error) {
	if f.metasErr != nil {
		return nil, f.metasErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]hoarderIface.InstanceInfo, 0)
	for _, h := range hashes {
		if info, ok := f.metas[h]; ok {
			out = append(out, info)
		}
	}
	return out, nil
}

func (f *fakeHoarder) ReplicasOf(hoarderIface.ResourceKind, string, string, string) ([]peerCore.ID, error) {
	return nil, nil
}
func (f *fakeHoarder) Close() {}

// fakeTns serves a canned projects index and per-scope config lists.
type fakeTns struct {
	tnsIface.Client
	indexKeys []string
	dbs       map[string]map[string]*structureSpec.Database // "pid/aid" → configs
	storages  map[string]map[string]*structureSpec.Storage
	lookupErr error
}

func (f *fakeTns) Lookup(tnsIface.Query) (interface{}, error) {
	if f.lookupErr != nil {
		return nil, f.lookupErr
	}
	return f.indexKeys, nil
}

func (f *fakeTns) Database() tnsIface.StructureIface[*structureSpec.Database] {
	return fakeStruct[*structureSpec.Database]{scoped: f.dbs}
}
func (f *fakeTns) Storage() tnsIface.StructureIface[*structureSpec.Storage] {
	return fakeStruct[*structureSpec.Storage]{scoped: f.storages}
}

type fakeStruct[T structureSpec.Structure] struct {
	tnsIface.StructureIface[T]
	scoped map[string]map[string]T
}

func (f fakeStruct[T]) All(pid, aid string, _ ...string) tnsIface.StructureGetter[T] {
	return fakeGetter[T]{list: f.scoped[pid+"/"+aid]}
}

type fakeGetter[T structureSpec.Structure] struct {
	tnsIface.StructureGetter[T]
	list map[string]T
}

func (f fakeGetter[T]) List() (map[string]T, string, string, error) {
	if f.list == nil {
		return nil, "", "", errors.New("no configs")
	}
	return f.list, "commit", "main", nil
}

// --- helpers ----------------------------------------------------------------

// seedLegacy writes a node-local kvdb namespace the way the previous
// architecture did — through pkg/kvdb (broadcaster and all), then closed.
func seedLegacy(t *testing.T, node peer.Node, hash string, entries map[string][]byte, deletes ...string) {
	t.Helper()
	factory := kvdb.New(node)
	db, err := factory.New(logger, hash, 5)
	if err != nil {
		t.Fatalf("seeding kvdb failed: %v", err)
	}
	ctx := context.Background()
	for k, v := range entries {
		if err := db.Put(ctx, k, v); err != nil {
			t.Fatalf("seed put failed: %v", err)
		}
	}
	for _, k := range deletes {
		if err := db.Delete(ctx, k); err != nil {
			t.Fatalf("seed delete failed: %v", err)
		}
	}
	db.Close()
}

func newTestMigrator(t *testing.T) (*Migrator, *fakeHoarder, *fakeTns) {
	t.Helper()
	node := peer.Mock(t.Context())
	h := newFakeHoarder()
	tns := &fakeTns{
		dbs:      map[string]map[string]*structureSpec.Database{},
		storages: map[string]map[string]*structureSpec.Storage{},
	}
	return New(t.Context(), node, h, tns), h, tns
}

func countPrefix(t *testing.T, m *Migrator, prefix string) int {
	t.Helper()
	keys, err := m.rawKeys(t.Context(), prefix)
	if err != nil {
		t.Fatalf("rawKeys(%s) failed: %v", prefix, err)
	}
	return len(keys)
}

// --- tests ------------------------------------------------------------------

func TestNamespacesAndLegacyView(t *testing.T) {
	m, _, _ := newTestMigrator(t)
	hash := mh.Hash("proj" + "" + "users")
	seedLegacy(t, m.node, hash, map[string][]byte{"a": []byte("1"), "b": []byte("2")}, "b")

	// Through the exported inspection surface (what tools use).
	hashes, err := Namespaces(t.Context(), m.node.Store())
	if err != nil || len(hashes) != 1 || hashes[0] != hash {
		t.Fatalf("namespaces = %v, err %v", hashes, err)
	}

	entries, err := Entries(t.Context(), m.node.Store(), hash)
	if err != nil {
		t.Fatalf("entries failed: %v", err)
	}
	if len(entries) != 1 || string(entries["/a"]) != "1" {
		t.Fatalf("tombstone semantics broken: %v", entries)
	}
	if got := FileCids(map[string][]byte{"/file/x/1": []byte("cid1"), "/other": []byte("x")}); len(got) != 1 || got[0] != "cid1" {
		t.Fatalf("FileCids wrong: %v", got)
	}
}

func TestBootDrainAndClose(t *testing.T) {
	m, h, tns := newTestMigrator(t)

	// Empty store: Boot is a no-op prefix query.
	if rep := m.Boot(); !rep.Empty() {
		t.Fatalf("boot on empty store not empty: %s", rep.Summary())
	}

	// An unresolvable namespace leaves Boot incomplete and arms the drain;
	// once the hoarder learns the identity (live first-touch), the drain
	// migrates it without further intervention.
	restore := DrainInterval
	DrainInterval = 50 * time.Millisecond
	defer func() { DrainInterval = restore }()

	hash := mh.Hash("p1" + "" + "runtime-only")
	seedLegacy(t, m.node, hash, map[string][]byte{"k": []byte("v")})
	tns.indexKeys = []string{"/projects/p1/x"}

	rep := m.Boot()
	if rep.RemainingCount() != 1 {
		t.Fatalf("boot should leave the unresolvable namespace: %s", rep.Summary())
	}
	if rep.Summary() == "" {
		t.Fatal("summary must not be empty")
	}

	h.metas[hash] = hoarderIface.InstanceInfo{
		Hash: hash, Kind: hoarderIface.Database,
		Meta: hoarderIface.MetaData{ProjectId: "p1", Match: "runtime-only"},
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if n := countPrefix(t, m, crdtPrefix+hash+"/"); n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("drain never migrated the instance")
		}
		time.Sleep(20 * time.Millisecond)
	}
	m.Close() // joins the drain

	if v, ok := h.instance(hash).data["/k"]; !ok || string(v) != "v" {
		t.Fatalf("drained value wrong: %q ok=%v", v, ok)
	}
}

func TestMigrate_ReplayFailureKeepsEverything(t *testing.T) {
	m, h, tns := newTestMigrator(t)

	hash := mh.Hash("p1" + "" + "users")
	seedLegacy(t, m.node, hash, map[string][]byte{"k1": []byte("v1")})
	tns.indexKeys = []string{"/projects/p1/x"}
	tns.dbs["p1/"] = map[string]*structureSpec.Database{"cfg": {Match: "users"}}
	h.instance(hash).failPut = true

	report := m.Migrate(t.Context())
	rep := report.Instances[hash]
	if rep == nil || rep.Err == "" || rep.Scrubbed {
		t.Fatalf("failed replay must keep the namespace: %+v", rep)
	}
	if n := countPrefix(t, m, crdtPrefix+hash+"/"); n == 0 {
		t.Fatal("namespace scrubbed despite replay failure")
	}
	if s := report.Summary(); s == "" {
		t.Fatal("summary must not be empty")
	}

	// Report error path in Summary too.
	errRep := &Report{Err: "boom"}
	if s := errRep.Summary(); !strings.Contains(s, "boom") {
		t.Fatalf("summary must carry the error: %s", s)
	}
}

func TestKindName(t *testing.T) {
	for kind, want := range map[hoarderIface.ResourceKind]string{
		hoarderIface.Database:        "database",
		hoarderIface.Storage:         "storage",
		hoarderIface.Global:          "global",
		hoarderIface.ResourceKind(9): "kind-9",
	} {
		if got := kindName(kind); got != want {
			t.Fatalf("kindName(%d) = %q, want %q", kind, got, want)
		}
	}
}

func TestResolve_Sources(t *testing.T) {
	m, h, tns := newTestMigrator(t)

	dbHash := mh.Hash("p1" + "" + "users")
	regexHash := mh.Hash("p1" + "" + "runtime-name")
	oldGlobal := mh.Hash("global" + "p1")
	tns.indexKeys = []string{"/projects/p1/somedb"}
	tns.dbs["p1/"] = map[string]*structureSpec.Database{
		"cfg-db": {Match: "users"},
		"cfg-rx": {Match: "^run.*", Regex: true},
	}

	resolved, unresolved, err := m.resolve(t.Context(), []string{dbHash, regexHash, oldGlobal})
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if info := resolved[dbHash]; info.Kind != hoarderIface.Database || info.Meta.Match != "users" || info.Meta.Branch != "main" {
		t.Fatalf("db identity wrong: %+v", info)
	}
	if info := resolved[oldGlobal]; info.Kind != hoarderIface.Global || info.Meta.ProjectId != "p1" {
		t.Fatalf("global identity wrong: %+v", info)
	}
	if len(unresolved) != 1 || unresolved[0] != regexHash {
		t.Fatalf("regex instance should be unresolved: %v", unresolved)
	}

	// Live traffic first-touched the regex instance: the hoarder now knows it.
	h.metas[regexHash] = hoarderIface.InstanceInfo{
		Hash: regexHash, Kind: hoarderIface.Database,
		Meta: hoarderIface.MetaData{ProjectId: "p1", Match: "runtime-name"},
	}
	resolved, unresolved, err = m.resolve(t.Context(), []string{regexHash})
	if err != nil || len(unresolved) != 0 {
		t.Fatalf("metas fallback failed: %v / %v", resolved, err)
	}

	// An outage carries everything over — nothing classified, error returned.
	tns.lookupErr = errors.New("tns down")
	if _, _, err = m.resolve(t.Context(), []string{dbHash}); err == nil {
		t.Fatal("lookup outage must abort the pass")
	}
}

func TestMigrate_DatabaseEndToEnd(t *testing.T) {
	m, h, tns := newTestMigrator(t)

	hash := mh.Hash("p1" + "" + "users")
	seedLegacy(t, m.node, hash, map[string][]byte{"k1": []byte("v1"), "k2": []byte("v2")})
	tns.indexKeys = []string{"/projects/p1/x"}
	tns.dbs["p1/"] = map[string]*structureSpec.Database{"cfg": {Match: "users"}}

	// A live write landed before the replay: it must win.
	h.instance(hash).data["/k1"] = []byte("live")

	report := m.Migrate(t.Context())
	rep := report.Instances[hash]
	if rep == nil || !rep.Verified || !rep.Scrubbed {
		t.Fatalf("instance not migrated: %+v (report err %q)", rep, report.Err)
	}
	if rep.Written != 1 || rep.Superseded != 1 {
		t.Fatalf("counts wrong: %+v", rep)
	}
	if got := h.instance(hash).data["/k1"]; !bytes.Equal(got, []byte("live")) {
		t.Fatalf("replay overwrote a live value: %q", got)
	}
	if got := h.instance(hash).data["/k2"]; !bytes.Equal(got, []byte("v2")) {
		t.Fatalf("replayed value wrong: %q", got)
	}
	if n := countPrefix(t, m, crdtPrefix+hash+"/"); n != 0 {
		t.Fatalf("namespace not scrubbed: %d keys left", n)
	}
	if report.RemainingCount() != 0 {
		t.Fatalf("nothing should remain: %s", report.Summary())
	}

	// Idempotency: a re-run finds nothing.
	if again := m.Migrate(t.Context()); !again.Empty() {
		t.Fatalf("re-run not empty: %s", again.Summary())
	}
}

func TestMigrate_StorageBytesGateAndSweep(t *testing.T) {
	m, h, tns := newTestMigrator(t)
	ctx := t.Context()

	// File bytes on the node, named by storage metadata.
	cid, err := m.node.AddFile(bytes.NewReader(bytes.Repeat([]byte("payload"), 100)))
	if err != nil {
		t.Fatalf("AddFile failed: %v", err)
	}
	hash := mh.Hash("p1" + "" + "files")
	seedLegacy(t, m.node, hash, map[string][]byte{
		"file/data/1": []byte(cid),
		"v/data":      []byte("1"),
	})
	tns.indexKeys = []string{"/projects/p1/x"}
	tns.storages["p1/"] = map[string]*structureSpec.Storage{"cfg": {Match: "files"}}

	// Fleet target of 2: one stash ack is not enough to drop local bytes.
	h.target = 2

	report := m.Migrate(ctx)
	rep := report.Instances[hash]
	if rep == nil || !rep.Verified {
		t.Fatalf("instance not verified: %+v (err %q)", rep, report.Err)
	}
	if rep.FilesStashed != 1 || !bytes.Equal(h.stashed[cid], bytes.Repeat([]byte("payload"), 100)) {
		t.Fatalf("bytes not stashed: %+v", rep)
	}
	if rep.Scrubbed || rep.FilesAwaitingRepl != 1 {
		t.Fatalf("under-replicated bytes must block scrub: %+v", rep)
	}
	if _, err := m.node.GetFile(ctx, cid); err != nil {
		t.Fatalf("local bytes must survive the sweep while under-replicated: %v", err)
	}

	// Fan-out completed: now the scrub and sweep may proceed.
	h.claims[cid] = 2
	report = m.Migrate(ctx)
	rep = report.Instances[hash]
	if rep == nil || !rep.Scrubbed {
		t.Fatalf("instance not scrubbed after claims reached target: %+v", rep)
	}
	if n := countPrefix(t, m, crdtPrefix); n != 0 {
		t.Fatalf("namespaces left: %d", n)
	}
	if n := countPrefix(t, m, blocksPrefix); n != 0 {
		t.Fatalf("blocks left after sweep: %d", n)
	}
}

func TestMigrate_UnresolvedKeptAndProtected(t *testing.T) {
	m, h, tns := newTestMigrator(t)
	ctx := t.Context()

	// A storage-shaped namespace nothing can name yet (regex, never touched):
	// its metadata AND its file bytes must survive every pass.
	cid, err := m.node.AddFile(bytes.NewReader([]byte("do not lose me")))
	if err != nil {
		t.Fatalf("AddFile failed: %v", err)
	}
	hash := mh.Hash("p1" + "" + "mystery")
	seedLegacy(t, m.node, hash, map[string][]byte{"file/x/1": []byte(cid)})
	tns.indexKeys = []string{"/projects/p1/x"}

	report := m.Migrate(ctx)
	if len(report.Unresolved) != 1 || report.Unresolved[0] != hash {
		t.Fatalf("expected 1 unresolved: %+v", report)
	}
	if n := countPrefix(t, m, crdtPrefix+hash+"/"); n == 0 {
		t.Fatal("unresolved namespace was touched")
	}
	if _, err := m.node.GetFile(ctx, cid); err != nil {
		t.Fatalf("unresolved instance's bytes were swept: %v", err)
	}

	// First-touch happened: the drain pass resolves and migrates it.
	h.metas[hash] = hoarderIface.InstanceInfo{
		Hash: hash, Kind: hoarderIface.Storage,
		Meta: hoarderIface.MetaData{ProjectId: "p1", Match: "mystery"},
	}
	report = m.Migrate(ctx)
	if rep := report.Instances[hash]; rep == nil || !rep.Scrubbed {
		t.Fatalf("drained instance not scrubbed: %+v (err %q)", rep, report.Err)
	}
}

func TestMigrate_VerifyFailureBlocksScrub(t *testing.T) {
	m, h, tns := newTestMigrator(t)

	hash := mh.Hash("p1" + "" + "users")
	seedLegacy(t, m.node, hash, map[string][]byte{"k1": []byte("v1")})
	tns.indexKeys = []string{"/projects/p1/x"}
	tns.dbs["p1/"] = map[string]*structureSpec.Database{"cfg": {Match: "users"}}

	h.instance(hash).dropOnVerify = "/k1"

	report := m.Migrate(t.Context())
	rep := report.Instances[hash]
	if rep == nil || rep.Verified || rep.Scrubbed {
		t.Fatalf("verify failure must block scrub: %+v", rep)
	}
	if n := countPrefix(t, m, crdtPrefix+hash+"/"); n == 0 {
		t.Fatal("unverified namespace was scrubbed")
	}
	if report.RemainingCount() != 1 {
		t.Fatalf("instance must remain: %s", report.Summary())
	}
}

// TestNoLiveBroadcaster guards the broadcaster-leak hazard structurally: the
// migration package must never open a namespace with a live broadcaster — the
// kvdb factory (kvdb.New) or a PubSub broadcaster constructor would announce the
// plaintext local heads to any live replica of the same path. Reads must go
// through the offline view: kvdb.NewDatastore with a nil broadcaster.
//
// go-ds-crdt is vendored into pkg/kvdb, so migration now legitimately imports
// pkg/kvdb for the offline datastore primitives; the guard targets the leak
// vectors directly instead of banning the import wholesale.
func TestNoLiveBroadcaster(t *testing.T) {
	forbidden := map[string]bool{
		"New":                       true, // factory -> attaches a live broadcaster
		"NewPubSubBroadcaster":      true,
		"NewBasicPubSubBroadcaster": true,
	}
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := parser.ParseFile(fset, f, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		kvdbName := ""
		for _, imp := range parsed.Imports {
			if strings.Trim(imp.Path.Value, `"`) == "github.com/taubyte/tau/pkg/kvdb" {
				if imp.Name != nil {
					kvdbName = imp.Name.Name
				} else {
					kvdbName = "kvdb"
				}
			}
		}
		if kvdbName == "" {
			continue
		}
		ast.Inspect(parsed, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if x, ok := sel.X.(*ast.Ident); ok && x.Name == kvdbName && forbidden[sel.Sel.Name] {
				t.Errorf("%s uses kvdb.%s — migration must read the offline view (kvdb.NewDatastore, nil broadcaster), never a live broadcaster", f, sel.Sel.Name)
			}
			return true
		})
	}
}
