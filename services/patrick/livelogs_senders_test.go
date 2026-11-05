package service

import (
	"context"
	"testing"

	"github.com/fxamacker/cbor/v2"
	"github.com/libp2p/go-libp2p/core/peer"
)

func assign(t *testing.T, db *fakeJobs, jid string, monkey peer.ID) {
	t.Helper()
	data, err := cbor.Marshal(Assignment{MonkeyPID: monkey.String()})
	if err != nil {
		t.Fatal(err)
	}
	db.lock.Lock()
	defer db.lock.Unlock()
	db.keys["/assigned/"+jid] = data
}

func TestLiveLogSenders(t *testing.T) {
	ctx := context.Background()
	monkey, other := peer.ID("monkey-one"), peer.ID("someone-else")
	db := &fakeJobs{keys: map[string][]byte{}}
	assign(t, db, "j", monkey)
	var s liveLogSenders

	if !s.allowed(ctx, db, "j", monkey) {
		t.Fatal("the job's monkey was refused")
	}
	if s.allowed(ctx, db, "j", other) {
		t.Fatal("a peer the job is not assigned to was accepted")
	}
	if s.allowed(ctx, db, "unassigned", monkey) {
		t.Fatal("a job assigned to nobody was accepted")
	}
	if s.allowed(ctx, db, "j", "") {
		t.Fatal("a message without a sender was accepted")
	}

	// the report came in and the assignment is gone: the last chunks still count
	db.lock.Lock()
	delete(db.keys, "/assigned/j")
	db.lock.Unlock()
	if !s.allowed(ctx, db, "j", monkey) {
		t.Fatal("the monkey's late chunks were refused")
	}
	if s.allowed(ctx, db, "j", other) {
		t.Fatal("another peer was accepted once the assignment was gone")
	}

	// the job is handed to another monkey, which then publishes
	second := peer.ID("monkey-two")
	assign(t, db, "j", second)
	if !s.allowed(ctx, db, "j", second) {
		t.Fatal("the job's new monkey was refused")
	}
}
