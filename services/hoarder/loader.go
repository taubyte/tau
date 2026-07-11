package hoarder

import (
	"sync"
	"time"

	"github.com/taubyte/tau/core/kvdb"
	hoarderSpecs "github.com/taubyte/tau/pkg/specs/hoarder"
)

// dataRebroadcastSec is the CRDT rebroadcast interval for a loaded instance's
// kvdb. Loading an instance opens its kvdb at path == instance hash — the same
// path substrate uses — so the hoarder replica joins the exact same CRDT.
const dataRebroadcastSec = 5

type loadedInstance struct {
	handle   kvdb.KVDB
	lastUsed time.Time
}

// loader holds per-node runtime state. Never persisted — loaded/lastUsed and
// the claimed set are in-memory only; the shared registry records claims (the
// durable truth), while `claimed` is this node's fast local view of them.
type loader struct {
	lock    sync.Mutex
	loaded  map[string]*loadedInstance
	claimed map[string]bool
	// writeLocks serialize an instance's local write commits (put/putnx/delete/
	// batch) so putnx's check-and-write is atomic against concurrent writers on
	// this node. Locks live for the process — tying them to load/unload would
	// let a racing writer hold a stale lock while a fresh one is handed out.
	writeLocks map[string]*sync.Mutex
}

func newLoader() *loader {
	return &loader{
		loaded:     make(map[string]*loadedInstance),
		claimed:    make(map[string]bool),
		writeLocks: make(map[string]*sync.Mutex),
	}
}

// writeLock returns the per-instance write mutex. Held across the LOCAL commit
// only — never across the replication barrier's network round-trip.
func (srv *Service) writeLock(hash string) *sync.Mutex {
	srv.ldr.lock.Lock()
	defer srv.ldr.lock.Unlock()
	mu, ok := srv.ldr.writeLocks[hash]
	if !ok {
		mu = new(sync.Mutex)
		srv.ldr.writeLocks[hash] = mu
	}
	return mu
}

func (srv *Service) markClaimed(hash string) {
	srv.ldr.lock.Lock()
	defer srv.ldr.lock.Unlock()
	srv.ldr.claimed[hash] = true
}

func (srv *Service) unmarkClaimed(hash string) {
	srv.ldr.lock.Lock()
	defer srv.ldr.lock.Unlock()
	delete(srv.ldr.claimed, hash)
}

func (srv *Service) isClaimed(hash string) bool {
	srv.ldr.lock.Lock()
	defer srv.ldr.lock.Unlock()
	return srv.ldr.claimed[hash]
}

func (srv *Service) claimedHashes() []string {
	srv.ldr.lock.Lock()
	defer srv.ldr.lock.Unlock()
	out := make([]string, 0, len(srv.ldr.claimed))
	for h := range srv.ldr.claimed {
		out = append(out, h)
	}
	return out
}

// load opens (or returns the already-open) kvdb handle for an instance and
// marks it used now.
func (srv *Service) load(hash string) (kvdb.KVDB, error) {
	srv.ldr.lock.Lock()
	defer srv.ldr.lock.Unlock()

	if inst, ok := srv.ldr.loaded[hash]; ok {
		inst.lastUsed = time.Now()
		return inst.handle, nil
	}

	handle, err := srv.dbFactory.New(logger, hash, dataRebroadcastSec)
	if err != nil {
		return nil, err
	}
	srv.ldr.loaded[hash] = &loadedInstance{handle: handle, lastUsed: time.Now()}
	return handle, nil
}

// unload closes an instance's kvdb handle. Claims persist across unload —
// unload is not un-replicate; the next head rebroadcast triggers DAG catch-up
// on reload.
func (srv *Service) unload(hash string) {
	srv.ldr.lock.Lock()
	defer srv.ldr.lock.Unlock()
	if inst, ok := srv.ldr.loaded[hash]; ok {
		inst.handle.Close()
		delete(srv.ldr.loaded, hash)
	}
}

func (srv *Service) isLoaded(hash string) bool {
	srv.ldr.lock.Lock()
	defer srv.ldr.lock.Unlock()
	_, ok := srv.ldr.loaded[hash]
	return ok
}

// unloadAll closes every loaded instance kvdb. Called on shutdown so the
// per-instance CRDT goroutines stop (crdt.Close waits for them) before the
// node's datastore closes under them — otherwise a lingering CRDT goroutine
// panics "pebble: closed". Closes run concurrently but each fully completes.
func (srv *Service) unloadAll() {
	srv.ldr.lock.Lock()
	handles := make([]kvdb.KVDB, 0, len(srv.ldr.loaded))
	for h, inst := range srv.ldr.loaded {
		handles = append(handles, inst.handle)
		delete(srv.ldr.loaded, h)
	}
	srv.ldr.lock.Unlock()

	var wg sync.WaitGroup
	for _, h := range handles {
		wg.Add(1)
		go func(h kvdb.KVDB) {
			defer wg.Done()
			h.Close()
		}(h)
	}
	wg.Wait()
}

// idleUnloadSweep unloads instances idle longer than IdleTTL. (A last-write
// guard — hold off unloading until the CRDT head flushes — arrives with the
// write path in PR 2.)
func (srv *Service) idleUnloadSweep() {
	now := time.Now()

	srv.ldr.lock.Lock()
	stale := make([]string, 0)
	for h, inst := range srv.ldr.loaded {
		if now.Sub(inst.lastUsed) >= hoarderSpecs.IdleTTL {
			stale = append(stale, h)
		}
	}
	srv.ldr.lock.Unlock()

	for _, h := range stale {
		srv.unload(h)
	}
}
