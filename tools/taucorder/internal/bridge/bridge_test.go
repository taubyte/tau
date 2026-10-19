package bridge

import (
	"context"
	"testing"
	"time"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"google.golang.org/protobuf/proto"

	discoveryv2 "github.com/taubyte/tau/pkg/proto/gen/tau/discovery/v2"
	"github.com/taubyte/tau/pkg/stats"
	"github.com/taubyte/tau/services/discovery"
	"github.com/taubyte/tau/tools/taucorder/internal/fixture"
)

func gossip(t *testing.T, h host.Host) *pubsub.PubSub {
	t.Helper()
	ps, err := pubsub.NewGossipSub(context.Background(), h)
	if err != nil {
		t.Fatal(err)
	}
	return ps
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("never:", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestWatchAndPick(t *testing.T) {
	ctx := context.Background()
	me := fixture.Host(t)
	a, b, c := fixture.Host(t), fixture.Host(t), fixture.Host(t)
	cloud := fixture.NewCloud()
	var id = fixture.Serve(t, a, cloud.Register)
	fixture.Serve(t, b, cloud.Register)
	fixture.Serve(t, c, cloud.Register)

	psMe, psA := gossip(t, me), gossip(t, a)
	br, err := New(ctx, me, psMe, nil, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer br.Close()

	fixture.Connect(t, me, a)
	fixture.Connect(t, me, b)
	fixture.Connect(t, me, c)

	bus, err := discovery.NewBus(psA, "test")
	if err != nil {
		t.Fatal(err)
	}
	// a is busy, b is idle, c is never described; and one garbage message.
	report := func(p peer.ID, load float64) []byte {
		raw, _ := proto.Marshal(&discoveryv2.Report{Peer: p.String(), Incarnation: 1, Load: load, Ready: true, Serving: []string{"register"}})
		return raw
	}
	eventually(t, "reports heard", func() bool {
		bus.Publish(ctx, []byte{0xff, 0xff})
		bus.Publish(ctx, report(a.ID(), 0.9))
		bus.Publish(ctx, report(b.ID(), 0.1))
		return len(br.Nodes()) == 2
	})

	eventually(t, "all three speak register", func() bool {
		got, err := br.Pick(ctx, id)
		return err == nil && len(got) == 3
	})
	got, _ := br.Pick(ctx, id)
	if got[0] != b.ID() || got[1] != a.ID() || got[2] != c.ID() {
		t.Fatalf("order %v; want b, a, c", got)
	}
	if !br.Speaks(a.ID(), id) || br.Speaks(a.ID(), "/nope") {
		t.Fatal("Speaks")
	}

	br.Failed(id, b.ID())
	got, _ = br.Pick(ctx, id)
	for _, p := range got {
		if p == b.ID() {
			t.Fatal("a failed peer was picked again")
		}
	}
	if br.Host() != me {
		t.Fatal("Host")
	}
}

func TestRankStaleAndNotReady(t *testing.T) {
	me := fixture.Host(t)
	br, err := New(context.Background(), me, gossip(t, me), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	defer br.Close()
	x, y := fixture.Host(t).ID(), fixture.Host(t).ID()
	br.view.Observe(discoveryReport(x, false))
	br.view.Observe(discoveryReport(y, true))
	out := br.order([]peer.ID{x, y})
	if out[0] != y {
		t.Fatal("not-ready ranked first")
	}
}

func discoveryReport(p peer.ID, ready bool) stats.Report {
	return stats.Report{Peer: p.String(), Incarnation: 1, Ready: ready}
}

func TestPickTimesOut(t *testing.T) {
	old := PickWait
	PickWait = 200 * time.Millisecond
	defer func() { PickWait = old }()
	me := fixture.Host(t)
	br, err := New(context.Background(), me, gossip(t, me), nil, "x")
	if err != nil {
		t.Fatal(err)
	}
	defer br.Close()
	if _, err := br.Pick(context.Background(), "/tau/nobody/v2"); err == nil {
		t.Fatal("picked from nobody")
	}
	if _, err := br.Pick(context.Background(), ""); err == nil {
		t.Fatal("picked an empty protocol")
	}
	br.Failed("", me.ID()) // no discovery for an empty protocol: ignored
}

func TestNewRefuses(t *testing.T) {
	if _, err := New(context.Background(), nil, nil, nil, ""); err == nil {
		t.Fatal("no host")
	}
	if _, err := New(context.Background(), fixture.Host(t), nil, nil, ""); err == nil {
		t.Fatal("no pubsub")
	}
}

func TestJoinRefuses(t *testing.T) {
	if _, err := Join(context.Background(), Config{}); err == nil {
		t.Fatal("joined without a swarm key")
	}
	if _, err := Join(context.Background(), Config{SwarmKey: []byte("k")}); err == nil {
		t.Fatal("joined without bootstrap")
	}
}
