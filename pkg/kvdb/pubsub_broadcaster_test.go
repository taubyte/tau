package kvdb

// Test for Item H: pubsub_broadcaster.go coverage using two in-process
// libp2p hosts connected directly to each other, each running their own
// GossipSub instance, exercising BasicPubSubBroadcaster end to end.

import (
	"context"
	"testing"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	host "github.com/libp2p/go-libp2p/core/host"
	peer "github.com/libp2p/go-libp2p/core/peer"
)

// twoConnectedHosts creates two in-process libp2p hosts listening on
// loopback-only addresses and connects them directly to each other.
func twoConnectedHosts(t *testing.T) (host.Host, host.Host) {
	t.Helper()
	ctx := context.Background()

	h1, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h1.Close() })

	h2, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h2.Close() })

	h1.Peerstore().AddAddrs(h2.ID(), h2.Addrs(), time.Hour)
	h2.Peerstore().AddAddrs(h1.ID(), h1.Addrs(), time.Hour)

	if err := h1.Connect(ctx, peer.AddrInfo{ID: h2.ID(), Addrs: h2.Addrs()}); err != nil {
		t.Fatal(err)
	}

	return h1, h2
}

// TestPubSubBroadcaster checks that a Broadcast() issued through one peer's
// BasicPubSubBroadcaster is observed via Next() on a different, directly
// connected peer subscribed to the same topic, and that Next() returns
// ErrNoMoreBroadcast once its context is cancelled.
func TestPubSubBroadcaster(t *testing.T) {
	h1, h2 := twoConnectedHosts(t)

	ps1, err := pubsub.NewGossipSub(context.Background(), h1)
	if err != nil {
		t.Fatal(err)
	}
	ps2, err := pubsub.NewGossipSub(context.Background(), h2)
	if err != nil {
		t.Fatal(err)
	}

	const topic = "crdt-pubsub-broadcaster-test"

	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	bc1, err := NewBasicPubSubBroadcaster(ctx1, ps1, topic)
	if err != nil {
		t.Fatal(err)
	}

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	bc2, err := NewBasicPubSubBroadcaster(ctx2, ps2, topic)
	if err != nil {
		t.Fatal(err)
	}

	// Poll (rather than sleep a fixed amount) until gossipsub has formed
	// its mesh between the two peers on the topic, otherwise an early
	// Publish() can be lost.
	deadline := time.Now().Add(10 * time.Second)
	for {
		if len(ps1.ListPeers(topic)) > 0 && len(ps2.ListPeers(topic)) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the pubsub mesh to form between the two peers")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Knowing about each other's subscription (ListPeers > 0) does not
	// guarantee gossipsub has finished grafting the mesh yet -- a
	// Publish() issued right after can still be dropped silently since
	// gossipsub only delivers to meshed peers. Rather than sleeping a
	// fixed extra amount (flaky/slow either way), retry the broadcast on
	// a short interval until bc2 actually observes it, bounded by an
	// overall deadline: this is deterministic and as fast as the mesh
	// allows.
	msg := []byte("hello-from-bc1")
	type nextResult struct {
		data []byte
		err  error
	}
	nextCh := make(chan nextResult, 1)
	go func() {
		got, err := bc2.Next(context.Background())
		nextCh <- nextResult{data: got, err: err}
	}()

	pubDeadline := time.Now().Add(10 * time.Second)
	var got []byte
waitLoop:
	for {
		select {
		case res := <-nextCh:
			if res.err != nil {
				t.Fatalf("Next() failed waiting for the broadcast message: %v", res.err)
			}
			got = res.data
			break waitLoop
		default:
		}
		if time.Now().After(pubDeadline) {
			t.Fatal("timed out waiting for the broadcast message despite retries")
		}
		if err := bc1.Broadcast(context.Background(), msg); err != nil {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if string(got) != string(msg) {
		t.Fatalf("expected %q, got %q", msg, got)
	}

	// Next() on an already-cancelled context returns ErrNoMoreBroadcast.
	cancelledCtx, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	if _, err := bc2.Next(cancelledCtx); err != ErrNoMoreBroadcast {
		t.Fatalf("expected ErrNoMoreBroadcast, got %v", err)
	}
}
