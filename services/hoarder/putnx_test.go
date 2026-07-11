package hoarder

import (
	"fmt"
	"sync"
	"testing"

	"github.com/taubyte/tau/p2p/streams/command"
	hoarderSpecs "github.com/taubyte/tau/pkg/specs/hoarder"
	"github.com/taubyte/tau/utils/maps"
)

func putnxBody(key string, value []byte) command.Body {
	return command.Body{
		hoarderSpecs.BodyKVOp:  hoarderSpecs.KVPutNx,
		hoarderSpecs.BodyKey:   key,
		hoarderSpecs.BodyValue: value,
	}
}

func putBody(key string, value []byte) command.Body {
	return command.Body{
		hoarderSpecs.BodyKVOp:  hoarderSpecs.KVPut,
		hoarderSpecs.BodyKey:   key,
		hoarderSpecs.BodyValue: value,
	}
}

func TestKVPutNx_Semantics(t *testing.T) {
	srv := newTestService(t)
	ctx := t.Context()
	hash := "putnx-test-instance"
	handle, err := srv.load(hash)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}

	// First write: key absent → written.
	resp, err := srv.kvPutNx(ctx, handle, hash, putnxBody("k", []byte("first")))
	if err != nil {
		t.Fatalf("putnx failed: %v", err)
	}
	if existed, _ := maps.Bool(resp, hoarderSpecs.BodyExisted); existed {
		t.Fatal("fresh key reported existed")
	}

	// Second write: key present → skipped, value unchanged.
	resp, err = srv.kvPutNx(ctx, handle, hash, putnxBody("k", []byte("second")))
	if err != nil {
		t.Fatalf("second putnx failed: %v", err)
	}
	if existed, _ := maps.Bool(resp, hoarderSpecs.BodyExisted); !existed {
		t.Fatal("present key not reported existed")
	}

	got, err := srv.kvGet(ctx, handle, command.Body{hoarderSpecs.BodyKey: "k"})
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if v, _ := maps.ByteArray(got, hoarderSpecs.BodyValue); string(v) != "first" {
		t.Fatalf("putnx overwrote an existing key: got %q", v)
	}
}

// TestKVPutNx_AtomicWithPut proves the per-instance write lock: a concurrent
// unconditional put must never end up shadowed by a putnx replaying an older
// value. In every legal interleaving the final value is the put's — the putnx
// either lands first (put overwrites it) or observes the key and skips. A torn
// interleaving (put commits between putnx's check and write) would leave the
// replayed value on top. Run with -race to also catch data races on the path.
func TestKVPutNx_AtomicWithPut(t *testing.T) {
	srv := newTestService(t)
	ctx := t.Context()
	hash := "putnx-race-instance"
	handle, err := srv.load(hash)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}

	for i := 0; i < 64; i++ {
		key := fmt.Sprintf("k-%d", i)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			if _, err := srv.kvPut(ctx, handle, hash, putBody(key, []byte("live"))); err != nil {
				t.Errorf("put %s failed: %v", key, err)
			}
		}()
		go func() {
			defer wg.Done()
			if _, err := srv.kvPutNx(ctx, handle, hash, putnxBody(key, []byte("replay"))); err != nil {
				t.Errorf("putnx %s failed: %v", key, err)
			}
		}()
		wg.Wait()

		got, err := srv.kvGet(ctx, handle, command.Body{hoarderSpecs.BodyKey: key})
		if err != nil {
			t.Fatalf("get %s failed: %v", key, err)
		}
		if v, _ := maps.ByteArray(got, hoarderSpecs.BodyValue); string(v) != "live" {
			t.Fatalf("key %s: replayed value shadowed a live write (got %q)", key, v)
		}
	}
}
