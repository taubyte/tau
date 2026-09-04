package auth

import (
	"testing"

	"github.com/libp2p/go-libp2p/core/host"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/proto/gen/tau/auth/v1/authv1connect"
)

// wireHarness is a running Service on one mocknet host reached from a second
// one over the real transport — connect-go, over go-libp2p-gostream, over
// libp2p's mocknet (service-conventions.md §0's measurement #1).
//
// This is the ONLY way any test in this package gets a caller identity a
// handler was not separately told about: rpc.RemotePeer reads a context
// value under a key private to package rpc (p2p/rpc/peer.go's
// remotePeerKey{}), so nothing outside that package — including every test
// here — can fabricate one. Every test that needs Register/Unregister to
// see a real caller, or that needs to prove a List RPC streams
// (connect.ServerStream has no exported constructor — handler_stream.go:
// "doesn't currently have an exported constructor"), goes through this.
type wireHarness struct {
	Service    *Service
	Store      kvdb.KVDB
	Mocknet    mocknet.Mocknet
	ServerHost host.Host
	CallerHost host.Host

	Projects     authv1connect.ProjectServiceClient
	Repositories authv1connect.RepositoryServiceClient
	Hooks        authv1connect.HookServiceClient
	Domains      authv1connect.DomainServiceClient
	Certificates authv1connect.CertificateServiceClient
}

func newWireHarness(t testing.TB, opts ...Option) *wireHarness {
	t.Helper()

	mn := newMocknet(t)
	server := newHost(t, mn)
	caller := newHost(t, mn)
	store := newStore(t)

	s, err := New(server, store, newTestConfig(t), opts...)
	require.NoError(t, err)
	require.NoError(t, s.Start(t.Context()))
	t.Cleanup(func() { s.Close() })

	client := dialAuth(t, caller, server)

	return &wireHarness{
		Service:      s,
		Store:        store,
		Mocknet:      mn,
		ServerHost:   server,
		CallerHost:   caller,
		Projects:     authv1connect.NewProjectServiceClient(client.HTTP(), client.URL()),
		Repositories: authv1connect.NewRepositoryServiceClient(client.HTTP(), client.URL()),
		Hooks:        authv1connect.NewHookServiceClient(client.HTTP(), client.URL()),
		Domains:      authv1connect.NewDomainServiceClient(client.HTTP(), client.URL()),
		Certificates: authv1connect.NewCertificateServiceClient(client.HTTP(), client.URL()),
	}
}

// dialAuth opens a p2p/rpc.Client from from to the harness's server, over
// the protocol this package's own Service listens on.
func dialAuth(t testing.TB, from, to host.Host) *rpc.Client {
	t.Helper()
	c, err := rpc.NewClient(from, to.ID(), Protocol)
	require.NoError(t, err)
	t.Cleanup(c.Close)
	return c
}

// secondCaller adds a third host to the harness's mocknet and returns a
// RepositoryServiceClient dialled from it — a second, genuinely different
// libp2p identity, for proving Register and Unregister deny a peer that is
// not the one that registered a repository.
func (h *wireHarness) secondCaller(t testing.TB) (host.Host, authv1connect.RepositoryServiceClient) {
	t.Helper()
	other := newHost(t, h.Mocknet)
	client := dialAuth(t, other, h.ServerHost)
	return other, authv1connect.NewRepositoryServiceClient(client.HTTP(), client.URL())
}
