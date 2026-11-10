package hoarder

import (
	"testing"

	"github.com/taubyte/tau/p2p/streams/command"
	hoarderSpecs "github.com/taubyte/tau/pkg/specs/hoarder"
)

// A batch refused for a bad op must leave nothing behind: the ops before it
// used to sit in the shared pending delta and go out with the next batch.
func TestRefusedBatchLeavesNothingForTheNext(t *testing.T) {
	srv := newTestService(t)
	ctx := t.Context()
	handle, err := srv.dbFactory.New(logger, "batch-leak", 5)
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()

	_, err = srv.applyBatch(ctx, handle, &dbUsage{}, command.Body{}, []interface{}{
		map[string]interface{}{hoarderSpecs.BodyKVOp: hoarderSpecs.KVPut, hoarderSpecs.BodyKey: "/refused", hoarderSpecs.BodyValue: []byte("x")},
		map[string]interface{}{hoarderSpecs.BodyKVOp: hoarderSpecs.KVPut}, // no key
	})
	if err == nil {
		t.Fatal("a batch with a malformed op was accepted")
	}

	if _, err = srv.applyBatch(ctx, handle, &dbUsage{}, command.Body{}, []interface{}{
		map[string]interface{}{hoarderSpecs.BodyKVOp: hoarderSpecs.KVPut, hoarderSpecs.BodyKey: "/next", hoarderSpecs.BodyValue: []byte("y")},
	}); err != nil {
		t.Fatal(err)
	}

	if v, err := handle.Get(ctx, "/refused"); err == nil && v != nil {
		t.Fatalf("a refused batch's op was published by the next batch: %q", v)
	}
	if v, err := handle.Get(ctx, "/next"); err != nil || string(v) != "y" {
		t.Fatalf("next batch not applied: %q %v", v, err)
	}
}
