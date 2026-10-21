package helpers

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	pebblev1 "github.com/cockroachdb/pebble"
	"github.com/ipfs/go-datastore"
	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p-kad-dht/dual"
	libp2pcrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/datastores/mem"
	"github.com/taubyte/tau/p2p/keypair"
)

func testHost(t *testing.T) (host.Host, *dual.DHT) {
	t.Helper()
	key, err := libp2pcrypto.UnmarshalPrivateKey(keypair.NewRaw())
	require.NoError(t, err)
	ds := mem.New()
	t.Cleanup(func() { ds.Close() })
	h, r, err := SetupLibp2p(context.Background(), key, []string{"/ip4/127.0.0.1/tcp/0"}, ds, nil, Libp2pSimpleNodeOptions...)
	require.NoError(t, err)
	t.Cleanup(func() { h.Close() })
	return h, r.(*dual.DHT)
}

func drain(ch <-chan peer.AddrInfo) []peer.AddrInfo {
	var out []peer.AddrInfo
	for p := range ch {
		out = append(out, p)
	}
	return out
}

// TestThePeerSourceOffersRelaysTheDHTKnows: autorelay asks it for candidates,
// and it answers with the closest peers that have an address.
func TestThePeerSourceOffersRelaysTheDHTKnows(t *testing.T) {
	ctx := context.Background()

	require.Empty(t, drain(newPeerSource(func() host.Host { return nil }, nil)(ctx, 1)), "no host yet")
	h, d := testHost(t)
	require.Empty(t, drain(newPeerSource(func() host.Host { return h }, func() *dual.DHT { return nil })(ctx, 1)), "no DHT yet")

	source := newPeerSource(func() host.Host { return h }, func() *dual.DHT { return d })
	require.Empty(t, drain(source(ctx, 1)), "a lone host knows nobody")

	// A WAN DHT that keeps loopback peers, so the lookup has someone to find.
	// Without the IP diversity filter too: every peer here is one address,
	// and it admits at most two of them per bucket, so three peers landing
	// in one bucket would leave one out.
	loopback := func(h host.Host) *dual.DHT {
		d, err := dual.New(ctx, h,
			dual.DHTOption(dht.Mode(dht.ModeServer)),
			dual.WanDHTOption(
				dht.QueryFilter(func(any, peer.AddrInfo) bool { return true }),
				dht.RoutingTableFilter(func(any, peer.ID) bool { return true }),
				dht.RoutingTablePeerDiversityFilter(nil),
			))
		require.NoError(t, err)
		t.Cleanup(func() { d.Close() })
		return d
	}
	bare := func() host.Host {
		h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
		require.NoError(t, err)
		t.Cleanup(func() { h.Close() })
		return h
	}
	h = bare()
	d = loopback(h)
	for range 3 {
		o := bare()
		od := loopback(o)
		require.NoError(t, h.Connect(ctx, peer.AddrInfo{ID: o.ID(), Addrs: o.Addrs()}))
		require.NoError(t, od.Bootstrap(ctx))
	}
	require.Eventually(t, func() bool { return d.WAN.RoutingTable().Size() == 3 }, 10*time.Second, 100*time.Millisecond)
	source = newPeerSource(func() host.Host { return h }, func() *dual.DHT { return d })

	offered := drain(source(ctx, 2))
	require.Len(t, offered, 2, "no more than asked for")
	for _, p := range offered {
		require.NotEmpty(t, p.Addrs)
	}
	require.Len(t, drain(source(ctx, 10)), 3, "every peer it knows when asked for more")

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	require.Empty(t, drain(source(cancelled, 2)))
}

func TestNewDatastoreMigratesAVersionOneStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store")
	v1, err := pebblev1.Open(path, nil)
	require.NoError(t, err)
	require.NoError(t, v1.Set([]byte("/k"), []byte("v"), pebblev1.Sync))
	require.NoError(t, v1.Close())

	ds, err := NewDatastore(path)
	require.NoError(t, err, "a store pebble v2 refuses is migrated, not refused")
	defer ds.Close()
	got, err := ds.Get(context.Background(), datastore.NewKey("/k"))
	require.NoError(t, err)
	require.Equal(t, []byte("v"), got)
}

func TestNewDatastoreRefusesWhatItCannotCleanUp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "store")
	require.NoError(t, os.MkdirAll(path+".v2", 0o755))
	require.NoError(t, os.Chmod(dir, 0o555))
	defer os.Chmod(dir, 0o755)
	if os.Geteuid() == 0 {
		t.Skip("root removes what a read-only directory holds")
	}
	_, err := NewDatastore(path)
	require.ErrorContains(t, err, "interrupted migration")
}
