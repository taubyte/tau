package hoarder

import (
	"strconv"
	"strings"
	"testing"

	"github.com/taubyte/tau/p2p/streams/command"
	hoarderSpecs "github.com/taubyte/tau/pkg/specs/hoarder"
	"github.com/taubyte/tau/utils/maps"
)

// sizedWrite is the batch substrate sends for one database write.
func sizedWrite(key string, n, max int) command.Body {
	return command.Body{
		hoarderSpecs.BodyKVOp:    hoarderSpecs.KVBatch,
		hoarderSpecs.BodyMaxSize: max,
		hoarderSpecs.BodyOps: []interface{}{
			map[string]interface{}{hoarderSpecs.BodyKVOp: hoarderSpecs.KVPut, hoarderSpecs.BodyKey: key, hoarderSpecs.BodyValue: []byte(strings.Repeat("x", n))},
			map[string]interface{}{hoarderSpecs.BodyKVOp: hoarderSpecs.KVPut, hoarderSpecs.BodyKey: "size" + key, hoarderSpecs.BodyValue: []byte(strconv.Itoa(n))},
		},
	}
}

func TestBatchQuota(t *testing.T) {
	srv := newTestService(t)
	ctx := t.Context()
	hash := "quota-test-instance"
	handle, err := srv.load(hash)
	if err != nil {
		t.Fatal(err)
	}
	write := func(body command.Body) string {
		t.Helper()
		resp, err := srv.kvBatch(ctx, handle, hash, body)
		if err != nil {
			t.Fatal(err)
		}
		return maps.TryString(resp, hoarderSpecs.BodyCode)
	}

	// A user key under size/ that is not a number is not a size record.
	if _, err := srv.kvPut(ctx, handle, hash, putBody("size/junk", []byte("not a number"))); err != nil {
		t.Fatal(err)
	}
	if code := write(sizedWrite("/a", 60, 100)); code != "" {
		t.Fatalf("first 60 bytes of 100 refused: %q", code)
	}
	if code := write(sizedWrite("/b", 60, 100)); code != hoarderSpecs.CodeOverCapacity {
		t.Fatalf("120 bytes of 100 accepted: %q", code)
	}
	if v, _ := handle.Get(ctx, "/b"); v != nil {
		t.Fatal("a refused write left its value")
	}
	if code := write(sizedWrite("/a", 50, 100)); code != "" {
		t.Fatalf("overwriting 60 bytes with 50 refused: %q", code)
	}
	if code := write(sizedWrite("/c", 50, 100)); code != "" {
		t.Fatalf("50+50 bytes of 100 refused: %q", code)
	}

	// The second copy of a write its first node accepted is never refused.
	copied := sizedWrite("/d", 10, 100)
	copied[hoarderSpecs.BodyNoBarrier] = true
	if code := write(copied); code != "" {
		t.Fatalf("K=2 copy refused: %q", code)
	}
	if u := srv.usageOf(hash); u.bytes != 110 {
		t.Fatalf("used = %d, want 110", u.bytes)
	}
}
