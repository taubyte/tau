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
	authv1 "github.com/taubyte/tau/pkg/v2/proto/gen/auth/v1"
	"github.com/taubyte/tau/pkg/v2/proto/gen/auth/v1/authv1connect"
)

// TestServiceNeedsOnlyAMocknetHostAndAnInMemoryStore is the narrow-dependency
// rule in its falsifiable form (.notes/v2/service-conventions.md, "The
// narrowest dependency is a REQUIREMENT"):
//
//	Can this service be constructed against mocknet.GenPeer() and an
//	in-memory store, with nothing else standing up?
//
// Everything it needs is written out inline here rather than taken from
// helpers_test.go, because the point of the test is exactly the list of
// things it does NOT do: no kvdb.Factory, no DHT, no IPFS DAG, no
// gossipsub, no temp directory, no config bag, no port. If someone later
// widens what New takes, this is the test that stops compiling, and it is
// meant to be read as the spec for the other seven services.
func TestServiceNeedsOnlyAMocknetHostAndAnInMemoryStore(t *testing.T) {
	mn := mocknet.New()
	defer mn.Close()

	server, err := mn.GenPeer()
	require.NoError(t, err)

	s, err := New(server, newStore(t), newTestConfig(t))
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

	_, err = authv1connect.NewProjectServiceClient(client.HTTP(), client.URL()).
		Get(t.Context(), connect.NewRequest(&authv1.ProjectRef{Id: "nope"}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err), "the answer came from the handler, not from a transport failure")
}

func TestNew_NoHost(t *testing.T) {
	_, err := New(nil, newStore(t), newTestConfig(t))
	assert.Error(t, err)
}

func TestNew_NoStore(t *testing.T) {
	_, err := New(newHost(t, newMocknet(t)), nil, newTestConfig(t))
	assert.Error(t, err)
}

func TestNew_EmptyDomainValidationKeys(t *testing.T) {
	valid := newTestConfig(t)
	for _, tc := range []struct {
		name string
		cfg  Config
	}{
		{"both empty", Config{}},
		{"private empty", Config{DomainValidation: KeyPair{Public: valid.DomainValidation.Public}}},
		{"public empty", Config{DomainValidation: KeyPair{Private: valid.DomainValidation.Private}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(newHost(t, newMocknet(t)), newStore(t), tc.cfg)
			assert.Error(t, err)
		})
	}
}

func TestNew_OptionFailure(t *testing.T) {
	_, err := New(newHost(t, newMocknet(t)), newStore(t), newTestConfig(t), func(*Service) error {
		return errors.New("deliberate option failure")
	})
	assert.Error(t, err)
}

// TestNew_BindsNothing is the observable half of service-conventions.md
// §2a: two Services can be constructed on the SAME host and protocol,
// because construction opens no listener. Start is what would collide.
func TestNew_BindsNothing(t *testing.T) {
	host := newHost(t, newMocknet(t))

	first, err := New(host, newStore(t), newTestConfig(t))
	require.NoError(t, err)
	defer first.Close()

	second, err := New(host, newStore(t), newTestConfig(t))
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

// TestClose_LeavesTheInjectedStoreUsable: the store was handed to New by
// whoever composed the node and is shared with the services composed beside
// it, so closing this service must not take it down. Store has no Close
// method (config.go), which makes that structural rather than a promise —
// this test is the observable half.
func TestClose_LeavesTheInjectedStoreUsable(t *testing.T) {
	store := newStore(t)
	s, err := New(newHost(t, newMocknet(t)), store, newTestConfig(t))
	require.NoError(t, err)
	require.NoError(t, s.Close())

	require.NoError(t, store.Put(t.Context(), "/probe", []byte("x")))
	got, err := store.Get(t.Context(), "/probe")
	require.NoError(t, err)
	assert.Equal(t, []byte("x"), got)
}

func TestWithServerOptions(t *testing.T) {
	s := newTestService(t, WithServerOptions(rpc.WithPrefaceTimeout(time.Second)))

	assert.Len(t, s.serverOptions, 1, "the ServerOption must reach the Service, for Start to pass to rpc.Listen")
	require.NoError(t, s.Start(t.Context()))
}

// TestStart_ListenFails reaches Start's own error return: p2p/rpc.Listen
// refuses a ServerOption that sets a field it owns (WithHTTPServer setting
// Handler — p2p/rpc/server.go's refuseOwnedFields), which is the cheapest
// way to make the real rpc.Listen call fail without needing a second
// listener already bound on the same protocol.
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

// TestStart_RefusedOnceTheFirstListenerIsGone and
// TestStart_RefusedAfterCloseWithoutStart are the two halves of "Start once"
// that TestStartAndClose cannot separate: there, each of Start's two guards
// is standing in front of the other, so removing either one on its own still
// leaves an error coming back. Neither of the states below is reachable with
// both guards in place, which is the point — Start's contract is its own,
// not something inherited from whatever rpc.Listen happens to refuse.

// Once the context Start was given ends, the server closes itself
// (p2p/rpc's watch) and hands the protocol back to the host — so rpc.Listen
// would happily bind a second time. Start must not: a Service that already
// ran is spent.
func TestStart_RefusedOnceTheFirstListenerIsGone(t *testing.T) {
	s := newTestService(t)

	ctx, cancel := context.WithCancel(t.Context())
	require.NoError(t, s.Start(ctx))
	cancel()
	<-s.server.Done()

	assert.Error(t, s.Start(t.Context()), "a spent Service must not quietly start a second listener")
}

// And a Service closed before it ever served has no listener to collide
// with, so only the closed flag can refuse it.
func TestStart_RefusedAfterCloseWithoutStart(t *testing.T) {
	s := newTestService(t)
	require.NoError(t, s.Close())
	assert.Error(t, s.Start(t.Context()))
}
