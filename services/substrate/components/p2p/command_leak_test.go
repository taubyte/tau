package p2p

import (
	"context"
	"runtime"
	"testing"
	"time"

	peercore "github.com/libp2p/go-libp2p/core/peer"
	iface "github.com/taubyte/tau/core/services/substrate/components/p2p"
	keypair "github.com/taubyte/tau/p2p/keypair"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/streams"
	"github.com/taubyte/tau/p2p/streams/client"
	"github.com/taubyte/tau/p2p/streams/command"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
	peerService "github.com/taubyte/tau/p2p/streams/service"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/services/substrate/components/p2p/stream"
)

// TestCommandSendNoGoroutineLeak reproduces the p2p Command.Send/SendTo
// goroutine leak: beforeSend used to construct a brand new p2p/streams/client
// (and its background discover goroutine) on every call instead of reusing
// one client for the Stream's lifetime. Sending many commands through a
// single Stream/Command must not grow the goroutine count roughly linearly
// with the number of sends.
func TestCommandSendNoGoroutineLeak(t *testing.T) {
	const protocol = "/leaktest/p2p/1.0"
	const cmdName = "echo"

	ctx := t.Context()

	receiver, err := peer.New(ctx, nil, keypair.NewRaw(), nil, []string{"/ip4/127.0.0.1/tcp/0"}, nil, true, false)
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Close()

	svr, err := peerService.New(receiver, "leak-test", protocol)
	if err != nil {
		t.Fatal(err)
	}
	defer svr.Stop()

	err = svr.Define(cmdName, func(context.Context, streams.Connection, command.Body) (cr.Response, error) {
		return cr.Response{"ok": true}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	svr.Start()

	sender, err := peer.New(ctx, nil, keypair.NewRaw(), nil, []string{"/ip4/127.0.0.1/tcp/0"}, nil, true, false)
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()

	if err := sender.Peer().Connect(ctx, peercore.AddrInfo{ID: receiver.ID(), Addrs: receiver.Peer().Addrs()}); err != nil {
		t.Fatal(err)
	}

	// One shared client, exactly as (*p2p.Service).p2pClient hands to every
	// Stream it creates.
	p2pClient, err := client.New(sender, protocol)
	if err != nil {
		t.Fatal(err)
	}
	defer p2pClient.Close()

	srv := NewTestService(sender)
	matcher := &iface.MatchDefinition{Project: "leak-test-project", Protocol: protocol}

	st, err := stream.New(srv, ctx, &structureSpec.Service{Id: "leak-test-service"}, "", matcher, p2pClient)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	cmd, err := st.Command(cmdName)
	if err != nil {
		t.Fatal(err)
	}

	send := func() error {
		_, err := cmd.Send(ctx, map[string]any{"data": []byte("ping")})
		return err
	}

	if err := send(); err != nil {
		t.Fatalf("warm-up send failed: %s", err)
	}

	for range 3 {
		runtime.GC()
		time.Sleep(50 * time.Millisecond)
	}
	before := runtime.NumGoroutine()

	const iterations = 50
	for i := range iterations {
		if err := send(); err != nil {
			t.Fatalf("send %d failed: %s", i, err)
		}
	}

	const maxGrowth = 15
	var after int
	settled := false
	for range 30 {
		runtime.GC()
		time.Sleep(50 * time.Millisecond)
		after = runtime.NumGoroutine()
		if after-before < maxGrowth {
			settled = true
			break
		}
	}

	if !settled {
		t.Fatalf("goroutine growth after %d sends through one Command: before=%d after=%d (grew by %d, want < %d)",
			iterations, before, after, after-before, maxGrowth)
	}
}
