package hoarder

import (
	"context"
	"errors"
	"testing"
	"time"

	peerCore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/tau/p2p/streams/command"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
	hoarderSpecs "github.com/taubyte/tau/pkg/specs/hoarder"
)

// The routing helpers touch only the sticky/replica state, so they're testable
// without a live hoarder (the network path is covered by the dream tests).

func TestRemoteKV_FailoverAndReset(t *testing.T) {
	r := &remoteKV{replicas: []peerCore.ID{"a", "b"}}

	if !r.failover() {
		t.Fatal("first failover should still have a replica to try")
	}
	if r.failover() {
		t.Fatal("second failover should exhaust the replicas")
	}

	r.reset()
	if r.replicas != nil || r.sticky != 0 {
		t.Fatalf("reset left state: replicas=%v sticky=%d", r.replicas, r.sticky)
	}
}

func TestRemoteKV_AdoptRedirect(t *testing.T) {
	const valid = "12D3KooWQFwFDkkGnQ8y23wTUZ1kV3RVpZWkTgy5rd3jyvvV2ypM"
	r := &remoteKV{}

	// A malformed id is dropped; a valid one is adopted and resets the sticky.
	r.sticky = 3
	r.adoptRedirect(cr.Response{hoarderSpecs.BodyPeers: []string{"not-a-peer", valid}})

	if len(r.replicas) != 1 || r.replicas[0].String() != valid {
		t.Fatalf("adoptRedirect = %v", r.replicas)
	}
	if r.sticky != 0 {
		t.Fatalf("adoptRedirect should reset sticky, got %d", r.sticky)
	}
}

func TestRemoteKV_InstanceBody(t *testing.T) {
	r := &remoteKV{project: "p", application: "a", match: "m", branch: "b"}
	body := r.instanceBody()
	if body[hoarderSpecs.BodyProject] != "p" || body[hoarderSpecs.BodyMatch] != "m" || body[hoarderSpecs.BodyBranch] != "b" {
		t.Fatalf("instanceBody = %v", body)
	}
}

// TestRemoteKV_DoRespectsCanceledContext pins that a caller who has given up does
// not pay the cold-start retry cost: do() checks ctx before each round, so a
// canceled ctx returns context.Canceled at once instead of sleeping through the
// backoff (worst case coldStartRetries*coldStartBackoff, ~6s). It short-circuits
// before any send, so no client/network is needed.
func TestRemoteKV_DoRespectsCanceledContext(t *testing.T) {
	r := &remoteKV{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	_, err := r.do(ctx, command.Body{hoarderSpecs.BodyKVOp: hoarderSpecs.KVGet, hoarderSpecs.BodyKey: "k"})
	elapsed := time.Since(start)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled ctx: got err %v, want context.Canceled", err)
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("canceled do() should return promptly, took %s (backoff not cancelled?)", elapsed)
	}
}
