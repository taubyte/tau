package hoarder

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	peerCore "github.com/libp2p/go-libp2p/core/peer"
	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
	"github.com/taubyte/tau/p2p/streams/command"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
	hoarderSpecs "github.com/taubyte/tau/pkg/specs/hoarder"
	"github.com/taubyte/tau/utils/maps"
)

// fakeHoarders stands in for the replicas of one instance. Each holds its own
// values, so a replica that was not written to is a lagging one.
type fakeHoarders struct {
	mu         sync.Mutex
	values     map[peerCore.ID]map[string][]byte
	calls      map[peerCore.ID]int
	down       map[peerCore.ID]bool
	notReplica map[peerCore.ID][]string
	anyPeer    peerCore.ID // answers a send that names no peer
}

func newFakeHoarders() *fakeHoarders {
	return &fakeHoarders{
		values:     map[peerCore.ID]map[string][]byte{},
		calls:      map[peerCore.ID]int{},
		down:       map[peerCore.ID]bool{},
		notReplica: map[peerCore.ID][]string{},
	}
}

func (f *fakeHoarders) set(pid peerCore.ID, key, value string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.values[pid] == nil {
		f.values[pid] = map[string][]byte{}
	}
	f.values[pid][canonicalKey(key)] = []byte(value)
}

func (f *fakeHoarders) count(pid peerCore.ID) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[pid]
}

func (f *fakeHoarders) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		n += c
	}
	return n
}

func (f *fakeHoarders) send(cmd string, body command.Body, peers ...peerCore.ID) (cr.Response, error) {
	pid := f.anyPeer
	if len(peers) > 0 {
		pid = peers[0]
	}
	f.mu.Lock()
	f.calls[pid]++
	down := f.down[pid]
	f.mu.Unlock()
	if down {
		return nil, errors.New("peer down")
	}
	return f.serve(pid, body), nil
}

// serve applies one op on one replica, as the hoarder's kvdbHandler would.
func (f *fakeHoarders) serve(pid peerCore.ID, body command.Body) cr.Response {
	f.mu.Lock()
	defer f.mu.Unlock()
	if peers, ok := f.notReplica[pid]; ok {
		return cr.Response{hoarderSpecs.BodyCode: hoarderSpecs.CodeNotReplica, hoarderSpecs.BodyPeers: peers}
	}
	if f.values[pid] == nil {
		f.values[pid] = map[string][]byte{}
	}
	resp := cr.Response{}
	apply := func(op map[string]interface{}) {
		key := canonicalKey(maps.TryString(op, hoarderSpecs.BodyKey))
		switch maps.TryString(op, hoarderSpecs.BodyKVOp) {
		case hoarderSpecs.KVGet:
			if v, ok := f.values[pid][key]; ok {
				resp[hoarderSpecs.BodyValue] = v
			} else {
				resp[hoarderSpecs.BodyCode] = hoarderSpecs.CodeNotFound
			}
		case hoarderSpecs.KVPut, hoarderSpecs.KVPutNx:
			v, _ := maps.ByteArray(op, hoarderSpecs.BodyValue)
			f.values[pid][key] = v
		case hoarderSpecs.KVDelete:
			delete(f.values[pid], key)
		}
	}
	if ops, ok := body[hoarderSpecs.BodyOps].([]interface{}); ok {
		for _, o := range ops {
			apply(o.(map[string]interface{}))
		}
	} else {
		apply(body)
	}
	resp[hoarderSpecs.BodyServedBy] = pid.String()
	return resp
}

func testPeers(t *testing.T) (a, b, c peerCore.ID) {
	t.Helper()
	ids := []string{
		"12D3KooWHS36LKeJVFCPb6g3i8VkZsMcMkwrV7Sg8Hh2pcP2LfHP",
		"12D3KooWGoShXGTy7asGYauUA1kLuLmHDovX5omu2Fjsy2NPFSq7",
		"12D3KooWQFwFDkkGnQ8y23wTUZ1kV3RVpZWkTgy5rd3jyvvV2ypM",
	}
	out := make([]peerCore.ID, len(ids))
	for i, id := range ids {
		pid, err := peerCore.Decode(id)
		if err != nil {
			t.Fatal(err)
		}
		out[i] = pid
	}
	return out[0], out[1], out[2]
}

func newTestKV(f *fakeHoarders, replicas ...peerCore.ID) *remoteKV {
	return &remoteKV{
		kind:    hoarderIface.Database,
		project: "p",
		match:   "m",
		send:    f.send,
		resolve: func() ([]peerCore.ID, error) { return replicas, nil },
	}
}

// warm waits for the handle's background replica lookup to land.
func warm(t *testing.T, r *remoteKV, want int) {
	t.Helper()
	r.readTarget("warm")
	deadline := time.Now().Add(2 * time.Second)
	for {
		r.mu.Lock()
		n := len(r.readers)
		r.mu.Unlock()
		if n == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("readers = %d, want %d", n, want)
		}
		time.Sleep(time.Millisecond)
	}
}

// The profile that started this: every node's Gets landed on one hoarder.
func TestRemoteKV_ReadsSpreadAcrossReplicas(t *testing.T) {
	a, b, c := testPeers(t)
	f := newFakeHoarders()
	for _, p := range []peerCore.ID{a, b, c} {
		f.set(p, "k", "v")
	}
	r := newTestKV(f, a, b, c)
	warm(t, r, 3)
	ctx := context.Background()

	for i := 0; i < 300; i++ {
		got, err := r.Get(ctx, "k")
		if err != nil || string(got) != "v" {
			t.Fatalf("Get = %q, %v", got, err)
		}
	}
	for _, p := range []peerCore.ID{a, b, c} {
		if n := f.count(p); n < 30 {
			t.Fatalf("replica %s served %d of 300 reads; a=%d b=%d c=%d", p, n, f.count(a), f.count(b), f.count(c))
		}
	}
}

func TestRemoteKV_LocalReplicaServesWithoutNetwork(t *testing.T) {
	a, b, c := testPeers(t)
	f := newFakeHoarders()
	f.set(b, "k", "local")
	var hits int
	var mu sync.Mutex
	defer ServeLocal(b, func(_ context.Context, body command.Body) (cr.Response, error) {
		mu.Lock()
		hits++
		mu.Unlock()
		return f.serve(b, body), nil
	})()

	r := newTestKV(f, a, b, c)
	warm(t, r, 3)
	for i := 0; i < 50; i++ {
		got, err := r.Get(context.Background(), "k")
		if err != nil || string(got) != "local" {
			t.Fatalf("Get = %q, %v", got, err)
		}
	}
	if n := f.total(); n != 0 {
		t.Fatalf("%d network calls, want none with a local replica", n)
	}
	if hits != 50 {
		t.Fatalf("local hoarder served %d of 50", hits)
	}
}

// Replicas other than the sticky one never receive the write here: they stand
// for CRDT delivery still in flight. The local one is the most tempting target.
func TestRemoteKV_ReadAfterWrite_LaggingReplica(t *testing.T) {
	a, b, c := testPeers(t)
	ctx := context.Background()
	writes := map[string]func(r *remoteKV) error{
		"put": func(r *remoteKV) error { return r.Put(ctx, "k", []byte("new")) },
		"putnx": func(r *remoteKV) error {
			_, err := r.PutNx(ctx, "k", []byte("new"))
			return err
		},
		"batch": func(r *remoteKV) error {
			batch, _ := r.BatchWithMax(ctx, 1024)
			if err := batch.Put("k", []byte("new")); err != nil {
				return err
			}
			return batch.Commit()
		},
	}
	for name, write := range writes {
		t.Run(name, func(t *testing.T) {
			f := newFakeHoarders()
			for _, p := range []peerCore.ID{a, b, c} {
				f.set(p, "k", "old")
			}
			defer ServeLocal(b, func(_ context.Context, body command.Body) (cr.Response, error) {
				return f.serve(b, body), nil
			})()
			r := newTestKV(f, a, b, c)
			warm(t, r, 3)

			if err := write(r); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 100; i++ {
				got, err := r.Get(ctx, "k")
				if err != nil || string(got) != "new" {
					t.Fatalf("read %d after write = %q, %v", i, got, err)
				}
			}

			// A key the handle did not write still spreads, and this one does
			// once the window has passed.
			r.mu.Lock()
			r.written[canonicalKey("k")] = time.Now().Add(-readYourWritesWindow)
			r.mu.Unlock()
			if got, _ := r.Get(ctx, "k"); string(got) != "old" {
				t.Fatalf("after the window the read should use the local replica, got %q", got)
			}
		})
	}
}

// A replica lagging behind a backlog can trail by more than a rebroadcast, so a
// written key stays on the sticky replica for minutes.
func TestRemoteKV_ReadAfterWrite_LaggingReplicaOverMinutes(t *testing.T) {
	a, b, c := testPeers(t)
	f := newFakeHoarders()
	for _, p := range []peerCore.ID{a, b, c} {
		f.set(p, "k", "old")
	}
	r := newTestKV(f, a, b, c)
	warm(t, r, 3)
	ctx := context.Background()

	if err := r.Put(ctx, "k", []byte("new")); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	r.written[canonicalKey("k")] = time.Now().Add(-time.Minute)
	r.mu.Unlock()
	for i := 0; i < 50; i++ {
		if got, err := r.Get(ctx, "k"); err != nil || string(got) != "new" {
			t.Fatalf("read %d one minute after the write = %q, %v", i, got, err)
		}
	}
}

// A handle that writes more keys than it can remember forgets them, but its
// reads of every key stay on the sticky replica for the window instead.
func TestRemoteKV_ReadAfterWrite_ManyKeys(t *testing.T) {
	a, b, c := testPeers(t)
	f := newFakeHoarders()
	for _, p := range []peerCore.ID{a, b, c} {
		f.set(p, "k", "old")
	}
	r := newTestKV(f, a, b, c)
	warm(t, r, 3)
	ctx := context.Background()

	if err := r.Put(ctx, "k", []byte("new")); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, maxWrittenKeys)
	for i := range keys {
		keys[i] = fmt.Sprintf("other/%d", i)
	}
	r.wrote(keys...)

	r.mu.Lock()
	remembered := len(r.written)
	r.mu.Unlock()
	if remembered > maxWrittenKeys {
		t.Fatalf("handle remembers %d written keys, bound is %d", remembered, maxWrittenKeys)
	}
	for i := 0; i < 50; i++ {
		if got, err := r.Get(ctx, "k"); err != nil || string(got) != "new" {
			t.Fatalf("read %d after the key was forgotten = %q, %v", i, got, err)
		}
	}
}

func TestRemoteKV_ReadAfterDelete_LaggingReplica(t *testing.T) {
	a, b, c := testPeers(t)
	f := newFakeHoarders()
	for _, p := range []peerCore.ID{a, b, c} {
		f.set(p, "k", "old")
	}
	r := newTestKV(f, a, b, c)
	warm(t, r, 3)
	ctx := context.Background()

	if err := r.Delete(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		if _, err := r.Get(ctx, "k"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("read %d after delete: %v, want ErrNotFound", i, err)
		}
	}
}

func TestRemoteKV_ReadFailover(t *testing.T) {
	a, b, c := testPeers(t)
	ctx := context.Background()

	t.Run("dead spread replica is dropped", func(t *testing.T) {
		f := newFakeHoarders()
		for _, p := range []peerCore.ID{a, b, c} {
			f.set(p, "k", "v")
		}
		f.down[b] = true
		r := newTestKV(f, a, b, c)
		warm(t, r, 3)
		for i := 0; i < 200; i++ {
			got, err := r.Get(ctx, "k")
			if err != nil || string(got) != "v" {
				t.Fatalf("Get = %q, %v", got, err)
			}
		}
		if n := f.count(b); n != 1 {
			t.Fatalf("dead replica tried %d times, want once", n)
		}
	})

	t.Run("dead sticky replica", func(t *testing.T) {
		f := newFakeHoarders()
		f.set(b, "k", "v")
		f.set(c, "k", "v")
		f.down[a] = true
		r := newTestKV(f, a, b, c)
		warm(t, r, 3)
		for i := 0; i < 50; i++ {
			got, err := r.Get(ctx, "k")
			if err != nil || string(got) != "v" {
				t.Fatalf("Get = %q, %v", got, err)
			}
		}
	})

	t.Run("not replica follows the redirect", func(t *testing.T) {
		f := newFakeHoarders()
		f.set(a, "k", "v")
		f.notReplica[b] = []string{a.String()}
		f.notReplica[c] = []string{a.String()}
		r := newTestKV(f, b, c)
		warm(t, r, 2)
		for i := 0; i < 20; i++ {
			got, err := r.Get(ctx, "k")
			if err != nil || string(got) != "v" {
				t.Fatalf("Get = %q, %v", got, err)
			}
		}
		// Each is asked once as a read target and at most once more as sticky.
		if n := f.count(b) + f.count(c); n > 4 {
			t.Fatalf("non-replicas asked %d times over 20 reads", n)
		}
	})
}

// A replica that lacks the key may be behind: a value written on another node
// and not yet delivered here must still be found.
func TestRemoteKV_MissAsksStickyReplica(t *testing.T) {
	a, b, c := testPeers(t)
	f := newFakeHoarders()
	f.set(a, "k", "v")
	r := newTestKV(f, a, b, c)
	warm(t, r, 3)
	ctx := context.Background()

	for i := 0; i < 50; i++ {
		got, err := r.Get(ctx, "k")
		if err != nil || string(got) != "v" {
			t.Fatalf("Get = %q, %v", got, err)
		}
	}
	if _, err := r.Get(ctx, "absent"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("absent key: %v, want ErrNotFound", err)
	}
}

func TestRemoteKV_WritesStayOnStickyReplica(t *testing.T) {
	a, b, c := testPeers(t)
	f := newFakeHoarders()
	r := newTestKV(f, a, b, c)
	warm(t, r, 3)
	ctx := context.Background()

	for i := 0; i < 20; i++ {
		key := fmt.Sprintf("k%d", i)
		if err := r.Put(ctx, key, []byte("v")); err != nil {
			t.Fatal(err)
		}
		if _, err := r.PutNx(ctx, key+"nx", []byte("v")); err != nil {
			t.Fatal(err)
		}
		if err := r.Delete(ctx, key); err != nil {
			t.Fatal(err)
		}
		batch, _ := r.BatchWithMax(ctx, 1024)
		if err := batch.Put(key+"b", []byte("v")); err != nil {
			t.Fatal(err)
		}
		if err := batch.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	if n := f.count(a); n != 80 {
		t.Fatalf("sticky replica got %d writes, want 80", n)
	}
	if nb, nc := f.count(b), f.count(c); nb != 0 || nc != 0 {
		t.Fatalf("writes reached other replicas: b=%d c=%d", nb, nc)
	}
}

// Quota inputs and storage metadata keep the sticky path: a stale size would
// misjudge the quota, and a stale version would be handed out twice.
func TestRemoteKV_QuotaAndStorageReadsStayOnStickyReplica(t *testing.T) {
	a, b, c := testPeers(t)
	ctx := context.Background()

	check := func(t *testing.T, r *remoteKV, f *fakeHoarders, key string) {
		t.Helper()
		f.set(a, key, "1")
		for i := 0; i < 30; i++ {
			if _, err := r.Get(ctx, key); err != nil {
				t.Fatal(err)
			}
		}
		if nb, nc := f.count(b), f.count(c); nb != 0 || nc != 0 {
			t.Fatalf("read left the sticky replica: b=%d c=%d", nb, nc)
		}
	}

	t.Run("size key", func(t *testing.T) {
		f := newFakeHoarders()
		r := newTestKV(f, a, b, c)
		warm(t, r, 3)
		check(t, r, f, "size/k")
	})

	t.Run("storage", func(t *testing.T) {
		f := newFakeHoarders()
		r := newTestKV(f, a, b, c)
		r.kind = hoarderIface.Storage
		// readTarget never asks for storage, so seed the set a lookup would give.
		r.readers = []peerCore.ID{a, b, c}
		check(t, r, f, "v/name")
	})
}

// An unplaced instance has no replicas yet: the op goes to any hoarder, which
// first-touches it, and the handle pins to the one that served.
func TestRemoteKV_FirstTouch(t *testing.T) {
	a, _, _ := testPeers(t)
	f := newFakeHoarders()
	f.anyPeer = a
	r := newTestKV(f)
	ctx := context.Background()

	if err := r.Put(ctx, "k", []byte("v")); err != nil {
		t.Fatal(err)
	}
	got, err := r.Get(ctx, "k")
	if err != nil || string(got) != "v" {
		t.Fatalf("Get = %q, %v", got, err)
	}
	if pid, ok := r.current(); !ok || pid != a {
		t.Fatalf("handle pinned to %q, want the hoarder that served", pid)
	}
	if _, err := r.Get(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing key: %v", err)
	}
}

func TestRemoteKV_ConcurrentReadsAndWrites_Race(t *testing.T) {
	a, b, c := testPeers(t)
	f := newFakeHoarders()
	for _, p := range []peerCore.ID{a, b, c} {
		f.set(p, "k", "v")
	}
	r := newTestKV(f, a, b, c)
	ctx := context.Background()

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				switch (w + i) % 4 {
				case 0:
					_ = r.Put(ctx, fmt.Sprintf("k%d", i%10), []byte("v"))
				case 1:
					r.dropReader(c)
				case 2:
					r.refreshReaders()
				default:
					if _, err := r.Get(ctx, "k"); err != nil {
						t.Error(err)
						return
					}
				}
			}
		}(w)
	}
	wg.Wait()
}

// The write's ack names who holds it. When that is this process's replica, the
// read is served here: the K=2 copy landed before the ack.
func TestRemoteKV_ReadAfterWrite_LocalCopyServes(t *testing.T) {
	a, b, c := testPeers(t)
	ctx := context.Background()
	f := newFakeHoarders()
	for _, p := range []peerCore.ID{a, b, c} {
		f.set(p, "k", "old")
	}
	var hits int
	var mu sync.Mutex
	defer ServeLocal(b, func(_ context.Context, body command.Body) (cr.Response, error) {
		mu.Lock()
		hits++
		mu.Unlock()
		return f.serve(b, body), nil
	})()

	r := newTestKV(f, a, b, c)
	r.send = func(cmd string, body command.Body, peers ...peerCore.ID) (cr.Response, error) {
		resp, err := f.send(cmd, body, peers...)
		if err == nil && maps.TryString(body, hoarderSpecs.BodyKVOp) == hoarderSpecs.KVBatch {
			f.serve(b, body) // the barrier's copy
			resp[hoarderSpecs.BodyCopiedTo] = b.String()
		}
		return resp, err
	}
	warm(t, r, 3)

	batch, _ := r.Batch(ctx)
	if err := batch.Put("k", []byte("new")); err != nil {
		t.Fatal(err)
	}
	if err := batch.Commit(); err != nil {
		t.Fatal(err)
	}
	remote := f.total()
	for i := 0; i < 20; i++ {
		if got, err := r.Get(ctx, "k"); err != nil || string(got) != "new" {
			t.Fatalf("read %d = %q, %v", i, got, err)
		}
	}
	if f.total() != remote {
		t.Fatalf("%d network reads, want none", f.total()-remote)
	}
	if hits != 20 {
		t.Fatalf("local replica served %d of 20", hits)
	}
}
