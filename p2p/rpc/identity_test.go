package rpc_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/rpc"
	echov1 "github.com/taubyte/tau/p2p/rpc/testdata/gen/echo/v1"
	"github.com/taubyte/tau/p2p/rpc/testdata/gen/echo/v1/echov1connect"
)

func TestRemotePeerIsTheDialler(t *testing.T) {
	h := newHarness(t)

	res, err := h.client.Whoami(t.Context(), connect.NewRequest(&echov1.EchoRequest{}))
	require.NoError(t, err)

	assert.Equal(t, h.clientH.ID().String(), res.Msg.Note, "rpc.RemotePeer")
	assert.Equal(t, h.clientH.ID().String(), string(res.Msg.Payload), "req.Peer().Addr")

	// It is a peer id, not a string that merely looks like one.
	id, err := peer.Decode(res.Msg.Note)
	require.NoError(t, err)
	assert.Equal(t, h.clientH.ID(), id)
	assert.NotEqual(t, h.serverH.ID(), id)
}

func TestRemotePeerIsPerCaller(t *testing.T) {
	h := newHarness(t)

	other := newHost(t)
	connectHosts(t, other, h.serverH)
	conn, err := rpc.NewClient(other, h.serverH.ID(), testProtocol)
	require.NoError(t, err)
	defer conn.Close()
	otherClient := echov1connect.NewEchoServiceClient(conn.HTTP(), conn.URL())

	mine, err := h.client.Whoami(t.Context(), connect.NewRequest(&echov1.EchoRequest{}))
	require.NoError(t, err)
	theirs, err := otherClient.Whoami(t.Context(), connect.NewRequest(&echov1.EchoRequest{}))
	require.NoError(t, err)

	assert.Equal(t, h.clientH.ID().String(), mine.Msg.Note)
	assert.Equal(t, other.ID().String(), theirs.Msg.Note)
	assert.NotEqual(t, mine.Msg.Note, theirs.Msg.Note)
}

// forger rewrites everything a caller is allowed to touch — every header it
// can think of, the authority, and the request's own RemoteAddr field — to
// claim it is someone else.
type forger struct {
	inner  http.RoundTripper
	claim  string
	victim string
}

func (f *forger) RoundTrip(r *http.Request) (*http.Response, error) {
	r.Header.Set("X-Forwarded-For", f.claim)
	r.Header.Set("X-Real-Ip", f.claim)
	r.Header.Set("X-Peer-Id", f.claim)
	r.Header.Set("Forwarded", "for="+f.claim)
	r.Header.Set("Connect-Peer", f.claim)
	// The authority the call is addressed to, and the field net/http fills in
	// on the server from the conn. Neither is the caller's to set.
	r.Host = f.victim
	r.URL.Host = f.victim
	r.RemoteAddr = f.claim
	return f.inner.RoundTrip(r)
}

func TestRemotePeerCannotBeForged(t *testing.T) {
	h := newHarness(t)

	// A real third host, so the claimed identity is a valid peer id that the
	// server could plausibly have believed.
	impersonated := newHost(t)

	conn, err := rpc.NewClient(h.clientH, h.serverH.ID(), testProtocol)
	require.NoError(t, err)
	defer conn.Close()

	lying := &http.Client{Transport: &forger{
		inner:  conn.HTTP().Transport,
		claim:  impersonated.ID().String(),
		victim: h.serverH.ID().String(),
	}}
	client := echov1connect.NewEchoServiceClient(lying, conn.URL())

	res, err := client.Whoami(t.Context(), connect.NewRequest(&echov1.EchoRequest{}))
	require.NoError(t, err)

	assert.Equal(t, h.clientH.ID().String(), res.Msg.Note,
		"the handler believed a forged identity")
	assert.Equal(t, h.clientH.ID().String(), string(res.Msg.Payload),
		"connect's peer address followed a forged identity")
	assert.NotEqual(t, impersonated.ID().String(), res.Msg.Note)
	assert.NotEqual(t, h.serverH.ID().String(), res.Msg.Note)
}

func TestRemotePeerFailsClosed(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle(echov1connect.NewEchoServiceHandler(newEchoService()))
	plain := httptest.NewServer(mux)
	defer plain.Close()

	client := echov1connect.NewEchoServiceClient(plain.Client(), plain.URL)
	_, err := client.Whoami(t.Context(), connect.NewRequest(&echov1.EchoRequest{}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	assert.Contains(t, err.Error(), rpc.ErrNoRemotePeer.Error())
}

func TestRemotePeerWithoutAContext(t *testing.T) {
	_, err := rpc.RemotePeer(context.Background())
	assert.ErrorIs(t, err, rpc.ErrNoRemotePeer)

	// A value of the wrong shape under some other key is not an identity.
	_, err = rpc.RemotePeer(context.WithValue(context.Background(), struct{}{}, peer.ID("x")))
	assert.ErrorIs(t, err, rpc.ErrNoRemotePeer)
}
