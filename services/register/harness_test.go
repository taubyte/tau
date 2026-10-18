package register

import (
	"context"
	"errors"
	"testing"

	"github.com/libp2p/go-libp2p/core/host"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/rpc"
	pkgregister "github.com/taubyte/tau/pkg/register"

	"github.com/taubyte/tau/pkg/proto/gen/tau/register/v2/registerv2connect"
)

// harness is a running Service on one mocknet host, reached from a second one
// over the real transport: connect-go, over go-libp2p-gostream, over libp2p's
// mocknet.
//
// EVERY TEST IN THIS PACKAGE GOES OVER THE WIRE, and that is the point of the
// package. What the register means is already proven in pkg/register against a
// map; what is left to prove here is that a request survives the round trip and
// that a failure arrives as the right status code — neither of which a direct
// handler call would show.
type harness struct {
	Service    *Service
	Store      pkgregister.Store
	Client     registerv2connect.RegisterServiceClient
	CallerHost host.Host
	ServerHost host.Host
}

func newHarness(t testing.TB, store pkgregister.Store) *harness {
	t.Helper()

	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })

	server := newHost(t, mn)
	caller := newHost(t, mn)

	s, err := New(server, WithStore(store))
	require.NoError(t, err)
	require.NoError(t, s.Start(t.Context()))
	t.Cleanup(func() { s.Close() })

	c, err := rpc.NewClient(caller, server.ID(), Protocol)
	require.NoError(t, err)
	t.Cleanup(c.Close)

	return &harness{
		Service:    s,
		Store:      store,
		Client:     registerv2connect.NewRegisterServiceClient(c.HTTP(), c.URL()),
		CallerHost: caller,
		ServerHost: server,
	}
}

// newClient is the typed client this package vends, dialled over the same
// mocknet as the raw one.
func newClient(t testing.TB, h *harness) *Client {
	t.Helper()
	c, err := NewClient(h.CallerHost, h.ServerHost.ID())
	require.NoError(t, err)
	t.Cleanup(c.Close)
	return c
}

func newHost(t testing.TB, mn mocknet.Mocknet) host.Host {
	t.Helper()
	h, err := mn.GenPeer()
	require.NoError(t, err)
	require.NoError(t, mn.LinkAll())
	return h
}

// brokenStore reads and writes as a store on the far side of an unreachable
// swarm does: it fails.
type brokenStore struct{ *pkgregister.MemStore }

var errUnreachable = errors.New("the store is unreachable")

func (brokenStore) Get(context.Context, string) ([]byte, bool, error) {
	return nil, false, errUnreachable
}
