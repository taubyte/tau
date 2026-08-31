package auth

import (
	"testing"

	"github.com/stretchr/testify/require"

	taupeer "github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/v2/proto/gen/auth/v1/authv1connect"
)

// wireHarness is a running Service on one mock node ("server") reached from
// a second mock node ("caller") over the real transport — connect-go, over
// go-libp2p-gostream, over the mocknet p2p/peer.Mock shares
// (service-conventions.md §0's measurement #1, reconfirmed for this
// package's own dependency versions before any test here was written on top
// of it: see helpers_test.go's newMockNode).
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
	ServerNode taupeer.Node
	CallerNode taupeer.Node

	Projects     authv1connect.ProjectServiceClient
	Repositories authv1connect.RepositoryServiceClient
	Hooks        authv1connect.HookServiceClient
	Domains      authv1connect.DomainServiceClient
	Certificates authv1connect.CertificateServiceClient
}

func newWireHarness(t testing.TB, opts ...Option) *wireHarness {
	t.Helper()

	server := newMockNode(t)
	caller := newMockNode(t)
	linkMockPeers(t)

	s, err := New(t.Context(), newTestConfig(t, server), opts...)
	require.NoError(t, err)
	require.NoError(t, s.Start(t.Context()))
	t.Cleanup(func() { s.Close() })

	client := dialAuth(t, caller, server)

	return &wireHarness{
		Service:      s,
		ServerNode:   server,
		CallerNode:   caller,
		Projects:     authv1connect.NewProjectServiceClient(client.HTTP(), client.URL()),
		Repositories: authv1connect.NewRepositoryServiceClient(client.HTTP(), client.URL()),
		Hooks:        authv1connect.NewHookServiceClient(client.HTTP(), client.URL()),
		Domains:      authv1connect.NewDomainServiceClient(client.HTTP(), client.URL()),
		Certificates: authv1connect.NewCertificateServiceClient(client.HTTP(), client.URL()),
	}
}

// dialAuth opens a p2p/rpc.Client from from to the harness's server, over
// the protocol this package's own Service listens on.
func dialAuth(t testing.TB, from, to taupeer.Node) *rpc.Client {
	t.Helper()
	c, err := rpc.NewClient(from.Peer(), to.Peer().ID(), Protocol)
	require.NoError(t, err)
	t.Cleanup(c.Close)
	return c
}

// secondCaller links a third node to the harness's server and returns a
// RepositoryServiceClient dialled from it — a second, genuinely different
// libp2p identity, for proving Unregister/Register deny a peer that is not
// the one that registered a repository.
func (h *wireHarness) secondCaller(t testing.TB) (taupeer.Node, authv1connect.RepositoryServiceClient) {
	t.Helper()
	other := newMockNode(t)
	linkMockPeers(t)
	client := dialAuth(t, other, h.ServerNode)
	return other, authv1connect.NewRepositoryServiceClient(client.HTTP(), client.URL())
}
