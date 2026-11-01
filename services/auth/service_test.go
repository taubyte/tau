package auth

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/rpc"
	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2/authv2connect"
)

func TestServiceNeedsOnlyAMocknetHost(t *testing.T) {
	mn := mocknet.New()
	defer mn.Close()

	server, err := mn.GenPeer()
	require.NoError(t, err)

	s, err := New(server)
	require.NoError(t, err)
	defer s.Close()

	// And it serves: constructed that way, it answers a real RPC from a
	// second peer over the real transport.
	require.NoError(t, s.Start(t.Context()))

	caller, err := mn.GenPeer()
	require.NoError(t, err)
	require.NoError(t, mn.LinkAll())

	client, err := rpc.NewClient(caller, server.ID(), Protocol)
	require.NoError(t, err)
	defer client.Close()

	_, err = authv2connect.NewIdentityServiceClient(client.HTTP(), client.URL()).
		Admit(t.Context(), connect.NewRequest(&authv2.Caller{Forge: "github.com"}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), "the answer came from the handler, not from a transport failure")
}

func TestNew_NoHost(t *testing.T) {
	_, err := New(nil)
	assert.Error(t, err)
}

func TestNew_OptionFailure(t *testing.T) {
	_, err := New(newHost(t, newMocknet(t)), func(*Service) error {
		return errors.New("deliberate option failure")
	})
	assert.Error(t, err)
}

func TestNew_BindsNothing(t *testing.T) {
	host := newHost(t, newMocknet(t))

	first, err := New(host)
	require.NoError(t, err)
	defer first.Close()

	second, err := New(host)
	require.NoError(t, err)
	defer second.Close()

	require.NoError(t, first.Start(t.Context()))
	assert.Error(t, second.Start(t.Context()), "the protocol is taken; only the second Start may find that out")
}

func TestStartAndClose(t *testing.T) {
	s := newTestService(t)

	require.NoError(t, s.Start(t.Context()))

	// Starting twice is an error, not a second listener.
	assert.Error(t, s.Start(t.Context()))

	require.NoError(t, s.Close())
	// Close is idempotent.
	assert.NoError(t, s.Close())

	// Start after Close is an error, not a listener nobody will ever stop.
	assert.Error(t, s.Start(t.Context()))
}

func TestClose_WithoutStart(t *testing.T) {
	s := newTestService(t)
	assert.NoError(t, s.Close())
	assert.NoError(t, s.Close())
}

func TestWithServerOptions(t *testing.T) {
	s := newTestService(t, WithServerOptions(rpc.WithPrefaceTimeout(time.Second)))

	assert.Len(t, s.serverOptions, 1, "the ServerOption must reach the Service, for Start to pass to rpc.Listen")
	require.NoError(t, s.Start(t.Context()))
}

func TestStart_ListenFails(t *testing.T) {
	s := newTestService(t, WithServerOptions(rpc.WithHTTPServer(func(hs *http.Server) {
		hs.Handler = http.NewServeMux()
	})))
	assert.Error(t, s.Start(t.Context()))
}

func TestDescriptor(t *testing.T) {
	assert.Equal(t, "auth", Descriptor.Name)
	assert.Equal(t, IngressNone, Descriptor.Ingress)
	assert.Equal(t, Protocol, Descriptor.Protocol)
}

func TestStart_RefusedOnceTheFirstListenerIsGone(t *testing.T) {
	s := newTestService(t)

	ctx, cancel := context.WithCancel(t.Context())
	require.NoError(t, s.Start(ctx))
	cancel()
	<-s.server.Done()

	assert.Error(t, s.Start(t.Context()), "a spent Service must not quietly start a second listener")
}

func TestStart_RefusedAfterCloseWithoutStart(t *testing.T) {
	s := newTestService(t)
	require.NoError(t, s.Close())
	assert.Error(t, s.Start(t.Context()))
}
