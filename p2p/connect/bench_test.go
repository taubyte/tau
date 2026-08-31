package connect_test

import (
	"context"
	"sync/atomic"
	"testing"

	connectrpc "connectrpc.com/connect"
	logging "github.com/ipfs/go-log/v2"
	"github.com/libp2p/go-libp2p/core/network"
	peercore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/taubyte/tau/p2p/connect"
	echov1 "github.com/taubyte/tau/p2p/connect/testdata/gen/echo/v1"
	"github.com/taubyte/tau/p2p/connect/testdata/gen/echo/v1/echov1connect"
	"github.com/taubyte/tau/p2p/keypair"
	tpeer "github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/streams"
	cbclient "github.com/taubyte/tau/p2p/streams/client"
	"github.com/taubyte/tau/p2p/streams/command"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
	cbservice "github.com/taubyte/tau/p2p/streams/service"
)

// BenchmarkConnectUnary and BenchmarkCborSend measure the same thing on the
// same machine: one small request and its reply, between two in-process
// libp2p hosts over loopback TCP.
//
// Both report streams/100calls, but they count it differently because the two
// stacks treat streams differently, and each count is exact for its stack.
// HTTP/2 keeps its stream open, so counting the live ones on the client is
// the whole answer. The cbor router closes the stream at the end of every
// command, so live streams would always read zero; the server counts how many
// times its stream handler ran instead.

func BenchmarkConnectUnary(b *testing.B) {
	logging.SetLogLevel("*", "error")

	h1, h2 := twoHosts(b)
	serve(b, h1, newEchoService())

	conn, err := connect.NewClient(h2, h1.ID(), testProtocol)
	if err != nil {
		b.Fatal(err)
	}
	defer conn.Close()
	client := echov1connect.NewEchoServiceClient(conn.HTTP(), conn.URL())

	msg := &echov1.EchoRequest{Payload: []byte("ping")}
	// Count from before the first call, so the stream every later call rides
	// on is counted rather than warmed away.
	before := liveStreams(h2, h1.ID(), testProtocol)
	if _, err := client.Unary(b.Context(), connectrpc.NewRequest(msg)); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for b.Loop() {
		if _, err := client.Unary(b.Context(), connectrpc.NewRequest(msg)); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	opened := liveStreams(h2, h1.ID(), testProtocol) - before
	b.ReportMetric(float64(opened), "streams")
	b.ReportMetric(float64(opened)*100/float64(b.N), "streams/100calls")
}

func BenchmarkCborSend(b *testing.B) {
	logging.SetLogLevel("*", "error")

	const path = "/tau/test/cbor/v1"
	ctx := b.Context()

	p1 := cborNode(b, ctx)
	p2 := cborNode(b, ctx)

	svc, err := cbservice.New(p1, "bench", path)
	if err != nil {
		b.Fatal(err)
	}
	defer svc.Stop()
	if err := svc.Define("echo", func(context.Context, streams.Connection, command.Body) (cr.Response, error) {
		return cr.Response{"message": "pong"}, nil
	}); err != nil {
		b.Fatal(err)
	}
	svc.Start()

	// Replace the handler the service just registered with one that counts
	// every stream before handing it to the same router.
	var handled atomic.Int64
	router := svc.Router()
	p1.Peer().SetStreamHandler(protocol.ID(path), func(s network.Stream) {
		handled.Add(1)
		router.Handle(streams.Stream(s))
	})

	if err := p2.Peer().Connect(ctx, peercore.AddrInfo{ID: p1.ID(), Addrs: p1.Peer().Addrs()}); err != nil {
		b.Fatal(err)
	}

	c, err := cbclient.New(p2, path)
	if err != nil {
		b.Fatal(err)
	}
	defer c.Close()

	body := command.Body{"message": "ping"}
	if _, err := c.Send("echo", body, p1.ID()); err != nil {
		b.Fatal(err) // warm the connection
	}

	before := handled.Load()
	b.ResetTimer()
	for b.Loop() {
		if _, err := c.Send("echo", body, p1.ID()); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	opened := handled.Load() - before
	b.ReportMetric(float64(opened), "streams")
	b.ReportMetric(float64(opened)*100/float64(b.N), "streams/100calls")
}

func cborNode(b *testing.B, ctx context.Context) tpeer.Node {
	b.Helper()
	n, err := tpeer.New(ctx, nil, keypair.NewRaw(), nil,
		[]string{"/ip4/127.0.0.1/tcp/0"}, nil, true, false)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { n.Close() })
	return n
}

func liveStreams(h interface{ Network() network.Network }, pid peercore.ID, proto protocol.ID) int {
	n := 0
	for _, c := range h.Network().ConnsToPeer(pid) {
		for _, s := range c.GetStreams() {
			if s.Protocol() == proto {
				n++
			}
		}
	}
	return n
}
