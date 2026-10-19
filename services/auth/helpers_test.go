package auth

import (
	"testing"

	"github.com/libp2p/go-libp2p/core/host"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"
)

// This package's whole test fixture is a mocknet host and a map. That is not
// a convenience, it is the narrow-dependency rule made falsifiable
// (config.go, .notes/v2/service-conventions.md): New takes host.Host, a
// Store interface and a plain Config, so there is no DHT, no IPFS DAG, no
// gossipsub, no temp directory and no kvdb.Factory anywhere below — and
// nothing here imports p2p/peer, whose Mock stands up all five before it can
// hand back a node.

// testLogger is the log.StandardLogger the in-memory kvdb factory wants. It
// is the only logger this package's tests construct, and nothing in the
// service itself takes one.

// newStore returns an in-memory store: a map behind kvdb.KVDB
// (pkg/kvdb/mock), which is what "a store interface, not a constructed
// store" buys — the service never sees the factory. It is typed kvdb.KVDB
// rather than Store so a test can still List a raw prefix to prove nothing
// was left behind; the service only ever sees the narrower Store.
func newMocknet(t testing.TB) mocknet.Mocknet {
	t.Helper()
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	return mn
}

// newHost adds a host to mn and links it to every host already there, which
// is all mocknet needs before a stream can be opened.
func newHost(t testing.TB, mn mocknet.Mocknet) host.Host {
	t.Helper()
	h, err := mn.GenPeer()
	require.NoError(t, err)
	require.NoError(t, mn.LinkAll())
	return h
}

// newTestService constructs (but does not Start) a Service, and hands back
// the store it was given so a test can seed rows through store.go's own row
// functions. Handler-level tests call a handler's methods directly —
// service-conventions.md §6a's "no wire, no server, no listener" — so
// construction alone is all most of this package's tests need; rpc_test.go
// and authz_test.go Start it themselves where a live listener and a second
// peer are the point.
func newTestService(t testing.TB, opts ...Option) *Service {
	t.Helper()
	s, err := New(newHost(t, newMocknet(t)), opts...)
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s
}
