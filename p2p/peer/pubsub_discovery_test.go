package peer

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/discovery"
	corepeer "github.com/libp2p/go-libp2p/core/peer"
	coretest "github.com/libp2p/go-libp2p/core/test"
	"github.com/libp2p/go-libp2p/p2p/discovery/backoff"
	routed "github.com/libp2p/go-libp2p/p2p/host/routed"
	ma "github.com/multiformats/go-multiaddr"
	"github.com/stretchr/testify/require"
)

type staticDiscovery struct{ peers []corepeer.AddrInfo }

func (s staticDiscovery) Advertise(context.Context, string, ...discovery.Option) (time.Duration, error) {
	return 0, nil
}

func (s staticDiscovery) FindPeers(context.Context, string, ...discovery.Option) (<-chan corepeer.AddrInfo, error) {
	ch := make(chan corepeer.AddrInfo, len(s.peers))
	for _, p := range s.peers {
		ch <- p
	}
	close(ch)
	return ch, nil
}

type countingRouter struct{ lookups atomic.Int32 }

func (r *countingRouter) FindPeer(context.Context, corepeer.ID) (corepeer.AddrInfo, error) {
	r.lookups.Add(1)
	return corepeer.AddrInfo{}, context.Canceled
}

func TestPubsubDiscoveryDoesNotLookUpAddresslessProviders(t *testing.T) {
	h, err := libp2p.New(libp2p.NoListenAddrs)
	require.NoError(t, err)
	defer h.Close()

	router := &countingRouter{}
	rh := routed.Wrap(h, router)

	addr, err := ma.NewMultiaddr("/ip4/192.0.2.1/tcp/4001")
	require.NoError(t, err)

	deadID, err := coretest.RandPeerID()
	require.NoError(t, err)
	withAddrs, err := coretest.RandPeerID()
	require.NoError(t, err)
	inStore, err := coretest.RandPeerID()
	require.NoError(t, err)
	h.Peerstore().AddAddr(inStore, addr, time.Hour)

	disc := dialableDiscovery{host: rh, Discovery: staticDiscovery{peers: []corepeer.AddrInfo{
		{ID: deadID},
		{ID: withAddrs, Addrs: []ma.Multiaddr{addr}},
		{ID: inStore},
	}}}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	ch, err := disc.FindPeers(ctx, "ns")
	require.NoError(t, err)
	var got []corepeer.ID
	for pi := range ch {
		got = append(got, pi.ID)
	}
	require.ElementsMatch(t, []corepeer.ID{withAddrs, inStore}, got)

	// The real connector and routed host: no DHT lookup may be started.
	connector, err := backoff.NewBackoffConnector(rh, 10, time.Second, backoff.NewFixedBackoff(time.Hour))
	require.NoError(t, err)
	ch, err = disc.FindPeers(ctx, "ns")
	require.NoError(t, err)
	connector.Connect(ctx, ch)
	time.Sleep(300 * time.Millisecond)
	require.Zero(t, router.lookups.Load())
}
