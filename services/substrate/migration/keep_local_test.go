package migration

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	ds "github.com/ipfs/go-datastore"
	peerCore "github.com/libp2p/go-libp2p/core/peer"
	coreKvdb "github.com/taubyte/tau/core/kvdb"
	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/kvdb"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	mh "github.com/taubyte/tau/utils/multihash"
)

// liveHandle is a hoarder-held instance: closing a handle does not unload it.
type liveHandle struct{ coreKvdb.KVDB }

func (liveHandle) Close() {}

func (l liveHandle) PutNx(ctx context.Context, key string, v []byte) (bool, error) {
	if _, err := l.KVDB.Get(ctx, key); err == nil {
		return true, nil
	}
	return false, l.KVDB.Put(ctx, key, v)
}

// sharedHoarder is a hoarder on the migrating node's own store: it opens an
// instance the way services/hoarder/loader.go does, at path == instance hash.
type sharedHoarder struct {
	*fakeHoarder
	factory  coreKvdb.Factory
	replicas []peerCore.ID

	lock sync.Mutex
	open map[string]coreKvdb.KVDB
}

func newSharedHoarder(t *testing.T, node peer.Node) *sharedHoarder {
	h := &sharedHoarder{fakeHoarder: newFakeHoarder(), factory: kvdb.New(node), open: make(map[string]coreKvdb.KVDB)}
	t.Cleanup(func() {
		for _, db := range h.open {
			db.Close()
		}
	})
	return h
}

func (h *sharedHoarder) KVDB(_ hoarderIface.ResourceKind, project, app, match, _ string) (coreKvdb.KVDB, error) {
	hash := mh.Hash(project + app + match)
	h.lock.Lock()
	defer h.lock.Unlock()
	if _, ok := h.open[hash]; !ok {
		db, err := h.factory.New(logger, hash, 5)
		if err != nil {
			return nil, err
		}
		h.open[hash] = db
	}
	return liveHandle{h.open[hash]}, nil
}

func (h *sharedHoarder) ReplicasOf(hoarderIface.ResourceKind, string, string, string) ([]peerCore.ID, error) {
	return h.replicas, nil
}

func read(t *testing.T, h *sharedHoarder, kind hoarderIface.ResourceKind, match, key string) string {
	t.Helper()
	kv, err := h.KVDB(kind, "p1", "", match, "")
	if err != nil {
		t.Fatal(err)
	}
	v, err := kv.Get(t.Context(), key)
	if err != nil {
		t.Fatalf("%s %s unreadable through the hoarder path: %v", match, key, err)
	}
	return string(v)
}

func sharedTns() *fakeTns {
	return &fakeTns{
		indexKeys: []string{"/projects/p1/x"},
		dbs:       map[string]map[string]*structureSpec.Database{"p1/": {"cfg": {Match: "users"}}},
		storages:  map[string]map[string]*structureSpec.Storage{},
	}
}

// The upgrade of a one-node cloud: pre-migration data on a store the hoarder
// shares must be readable afterwards, and nothing local may go.
func TestKeepLocal_PreMigrationDataReadableOnSharedStore(t *testing.T) {
	restore := DrainInterval
	DrainInterval = 20 * time.Millisecond
	defer func() { DrainInterval = restore }()

	node := peer.Mock(t.Context())
	h := newSharedHoarder(t, node)

	dbHash, oldGlobal := mh.Hash("p1"+""+"users"), mh.Hash("global"+"p1")
	seedLegacy(t, node, dbHash, map[string][]byte{"k": []byte("v")})
	seedLegacy(t, node, oldGlobal, map[string][]byte{"g": []byte("gv")})
	seedLegacy(t, node, "auth", map[string][]byte{"/github/repo/1": []byte("secret")})
	artifact := randomBytes(t, 1<<20)
	artifactCid, err := node.AddFile(bytes.NewReader(artifact))
	if err != nil {
		t.Fatal(err)
	}

	m := New(t.Context(), node, h, sharedTns(), KeepLocal())
	t.Cleanup(m.Close)
	blocksBefore, oldGlobalBefore := countPrefix(t, m, blocksPrefix), countPrefix(t, m, crdtPrefix+oldGlobal+"/")

	m.Boot()
	deadline := time.Now().Add(20 * time.Second)
	for {
		if done, _ := node.Store().Has(t.Context(), doneKey); done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the migration never finished on the shared node")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if v := read(t, h, hoarderIface.Database, "users", "/k"); v != "v" {
		t.Fatalf("database value = %q", v)
	}
	if v := read(t, h, hoarderIface.Global, "global", "/g"); v != "gv" {
		t.Fatalf("global value = %q", v)
	}

	if n := countPrefix(t, m, crdtPrefix+oldGlobal+"/"); n != oldGlobalBefore {
		t.Fatalf("legacy global namespace changed: %d -> %d keys", oldGlobalBefore, n)
	}
	if n := countPrefix(t, m, blocksPrefix); n < blocksBefore {
		t.Fatalf("blocks deleted on a shared store: %d -> %d", blocksBefore, n)
	}
	if got, err := readFile(t, node, artifactCid); err != nil || !bytes.Equal(got, artifact) {
		t.Fatalf("artifact lost: %v", err)
	}
	if v := mustEntries(t, m, "auth")["/github/repo/1"]; string(v) != "secret" {
		t.Fatalf("auth database lost its key: %q", v)
	}
}

// A namespace this node's hoarder holds is live: reading it needs no replay,
// and a view's Close would erase the live instance's crash marker.
func TestKeepLocal_LeavesTheHoardersOwnInstanceAlone(t *testing.T) {
	node := peer.Mock(t.Context())
	h := newSharedHoarder(t, node)
	h.replicas = []peerCore.ID{node.ID()}

	hash := mh.Hash("p1" + "" + "users")
	seedLegacy(t, node, hash, map[string][]byte{"k": []byte("v")})
	if v := read(t, h, hoarderIface.Database, "users", "/k"); v != "v" {
		t.Fatalf("value = %q", v)
	}
	crashMarker := ds.NewKey(crdtPrefix + hash + "/bs")

	m := New(t.Context(), node, h, sharedTns(), KeepLocal())
	t.Cleanup(m.Close)
	report := m.Migrate(t.Context())

	rep := report.Instances[hash]
	if rep == nil || !rep.Verified || rep.Written != 0 {
		t.Fatalf("held instance not left in place: %+v", rep)
	}
	if ok, err := node.Store().Has(t.Context(), crashMarker); err != nil || !ok {
		t.Fatalf("a view touched the live instance: crash marker present=%v err=%v", ok, err)
	}
}

func TestKeepLocal_ReplaysOnce(t *testing.T) {
	m, h, tns := newTestMigrator(t)
	m.keepLocal = true
	t.Cleanup(m.Close)
	hash := mh.Hash("p1" + "" + "users")
	seedLegacy(t, m.node, hash, map[string][]byte{"k": []byte("v")})
	tns.indexKeys = []string{"/projects/p1/x"}
	tns.dbs["p1/"] = map[string]*structureSpec.Database{"cfg": {Match: "users"}}

	first := m.Migrate(t.Context())
	if first.RemainingCount() != 0 || first.SweptKeys != 0 || string(h.instance(hash).data["/k"]) != "v" {
		t.Fatalf("first pass: %s", first.Summary())
	}
	h.instance(hash).failPut = true
	if again := m.Migrate(t.Context()); len(again.Instances) != 0 {
		t.Fatalf("a recorded replay ran again: %s", again.Summary())
	}
	if n := countPrefix(t, m, crdtPrefix+hash+"/"); n == 0 {
		t.Fatal("local data deleted")
	}
}
