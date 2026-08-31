package rpc_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/rpc"
	echov1 "github.com/taubyte/tau/p2p/rpc/testdata/gen/echo/v1"
	"github.com/taubyte/tau/p2p/rpc/testdata/gen/echo/v1/echov1connect"
)

const testProtocol = protocol.ID("/tau/test/echo/v1")

// echoService answers every shape the four connect stream kinds come in, and
// reports back what the handler could see of its caller.
type echoService struct {
	// gate holds Countdown after its first message, so a test can prove the
	// first arrived while the handler was still parked.
	gate chan struct{}
	// parked closes when Park is entered, and parkErr carries what Park saw
	// when its context ended.
	parked  chan struct{}
	parkErr chan error
}

func newEchoService() *echoService {
	return &echoService{
		gate:    make(chan struct{}),
		parked:  make(chan struct{}),
		parkErr: make(chan error, 1),
	}
}

func (s *echoService) Unary(ctx context.Context, req *connect.Request[echov1.EchoRequest]) (*connect.Response[echov1.EchoResponse], error) {
	return connect.NewResponse(&echov1.EchoResponse{Payload: req.Msg.Payload}), nil
}

func (s *echoService) Countdown(ctx context.Context, req *connect.Request[echov1.EchoRequest], stream *connect.ServerStream[echov1.EchoResponse]) error {
	n := int(req.Msg.Count)
	for i := range n {
		if i > 0 {
			// Everything after the first message waits for the test to say
			// it already has the first.
			select {
			case <-s.gate:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if err := stream.Send(&echov1.EchoResponse{Payload: req.Msg.Payload, Index: int32(i)}); err != nil {
			return err
		}
	}
	return nil
}

func (s *echoService) Absorb(ctx context.Context, stream *connect.ClientStream[echov1.EchoRequest]) (*connect.Response[echov1.EchoResponse], error) {
	sum := sha256.New()
	var total int
	for stream.Receive() {
		sum.Write(stream.Msg().Payload)
		total += len(stream.Msg().Payload)
	}
	if err := stream.Err(); err != nil {
		return nil, err
	}
	return connect.NewResponse(&echov1.EchoResponse{
		Note:  hex.EncodeToString(sum.Sum(nil)),
		Index: int32(total),
	}), nil
}

func (s *echoService) Volley(ctx context.Context, stream *connect.BidiStream[echov1.EchoRequest, echov1.EchoResponse]) error {
	for i := 0; ; i++ {
		msg, err := stream.Receive()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := stream.Send(&echov1.EchoResponse{Payload: msg.Payload, Index: int32(i)}); err != nil {
			return err
		}
	}
}

func (s *echoService) Whoami(ctx context.Context, req *connect.Request[echov1.EchoRequest]) (*connect.Response[echov1.EchoResponse], error) {
	res := &echov1.EchoResponse{
		// What connect reports as the peer address, verbatim.
		Payload: []byte(req.Peer().Addr),
	}
	id, err := rpc.RemotePeer(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}
	res.Note = id.String()
	return connect.NewResponse(res), nil
}

func (s *echoService) Fail(ctx context.Context, req *connect.Request[echov1.EchoRequest]) (*connect.Response[echov1.EchoResponse], error) {
	err := connect.NewError(connect.CodeResourceExhausted, errors.New("the well is dry"))
	err.Meta().Set("Echo-Reason", "deliberate")
	return nil, err
}

func (s *echoService) Park(ctx context.Context, req *connect.Request[echov1.EchoRequest]) (*connect.Response[echov1.EchoResponse], error) {
	close(s.parked)
	<-ctx.Done()
	s.parkErr <- ctx.Err()
	return nil, connect.NewError(connect.CodeCanceled, ctx.Err())
}

// twoHosts returns two in-process libp2p hosts, dialled to each other over a
// real TCP transport with a real security handshake.
func twoHosts(t testing.TB) (host.Host, host.Host) {
	t.Helper()
	h1, h2 := newHost(t), newHost(t)
	connectHosts(t, h2, h1)
	return h1, h2
}

func newHost(t testing.TB) host.Host {
	t.Helper()
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	t.Cleanup(func() { h.Close() })
	return h
}

func connectHosts(t testing.TB, from, to host.Host) {
	t.Helper()
	from.Peerstore().AddAddrs(to.ID(), to.Addrs(), time.Hour)
	require.NoError(t, from.Connect(t.Context(), peer.AddrInfo{ID: to.ID(), Addrs: to.Addrs()}))
}

// harness is a running echo server on one host and a client on another.
type harness struct {
	server  *rpc.Server
	client  echov1connect.EchoServiceClient
	conn    *rpc.Client
	svc     *echoService
	serverH host.Host
	clientH host.Host
}

func newHarness(t testing.TB) *harness {
	t.Helper()
	h1, h2 := twoHosts(t)
	svc := newEchoService()

	srv := serve(t, h1, svc)
	conn, err := rpc.NewClient(h2, h1.ID(), testProtocol)
	require.NoError(t, err)
	t.Cleanup(conn.Close)

	return &harness{
		server:  srv,
		client:  echov1connect.NewEchoServiceClient(conn.HTTP(), conn.URL()),
		conn:    conn,
		svc:     svc,
		serverH: h1,
		clientH: h2,
	}
}

func serve(t testing.TB, h host.Host, svc echov1connect.EchoServiceHandler, opts ...rpc.ServerOption) *rpc.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle(echov1connect.NewEchoServiceHandler(svc))
	srv, err := rpc.Listen(t.Context(), h, testProtocol, mux, opts...)
	require.NoError(t, err)
	t.Cleanup(func() { srv.Close() })
	return srv
}

// h2Only is what a client has to speak to reach one of these servers: the
// same protocol set the package puts on its http.Server, built here because
// a test outside the package cannot reach that one.
func h2Only() *http.Protocols {
	p := new(http.Protocols)
	p.SetUnencryptedHTTP2(true)
	return p
}
