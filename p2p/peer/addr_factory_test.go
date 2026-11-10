package peer

import (
	"context"
	"testing"

	"github.com/libp2p/go-libp2p/config"
	ma "github.com/multiformats/go-multiaddr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/p2p/keypair"
)

func TestMakeAddrsFactory_AnnounceOnly(t *testing.T) {
	announce := []string{"/ip4/1.2.3.4/tcp/4001", "/ip4/5.6.7.8/tcp/4001"}
	noAnnounce := []string{}

	factory, err := makeAddrsFactory(announce, noAnnounce)
	require.NoError(t, err)
	require.NotNil(t, factory)

	inputAddrs := []ma.Multiaddr{
		mustMultiaddr(t, "/ip4/127.0.0.1/tcp/4001"),
		mustMultiaddr(t, "/ip4/192.168.1.1/tcp/4001"),
	}

	result := factory(inputAddrs)

	assert.Len(t, result, 2)
	assert.Equal(t, "/ip4/1.2.3.4/tcp/4001", result[0].String())
	assert.Equal(t, "/ip4/5.6.7.8/tcp/4001", result[1].String())
}

func TestMakeAddrsFactory_NoAnnounce(t *testing.T) {
	announce := []string{}
	noAnnounce := []string{}

	factory, err := makeAddrsFactory(announce, noAnnounce)
	require.NoError(t, err)
	require.NotNil(t, factory)

	inputAddrs := []ma.Multiaddr{
		mustMultiaddr(t, "/ip4/127.0.0.1/tcp/4001"),
		mustMultiaddr(t, "/ip4/192.168.1.1/tcp/4001"),
	}

	result := factory(inputAddrs)

	assert.Len(t, result, 2)
}

func TestMakeAddrsFactory_WithNoAnnounceList(t *testing.T) {
	announce := []string{}
	noAnnounce := []string{"/ip4/127.0.0.1/tcp/4001"}

	factory, err := makeAddrsFactory(announce, noAnnounce)
	require.NoError(t, err)
	require.NotNil(t, factory)

	inputAddrs := []ma.Multiaddr{
		mustMultiaddr(t, "/ip4/127.0.0.1/tcp/4001"),
		mustMultiaddr(t, "/ip4/192.168.1.1/tcp/4001"),
	}

	result := factory(inputAddrs)

	assert.Len(t, result, 1)
	assert.Equal(t, "/ip4/192.168.1.1/tcp/4001", result[0].String())
}

func TestMakeAddrsFactory_InvalidAnnounce(t *testing.T) {
	announce := []string{"not-a-valid-multiaddr"}
	noAnnounce := []string{}

	_, err := makeAddrsFactory(announce, noAnnounce)
	assert.Error(t, err)
}

func TestMakeAddrsFactory_InvalidNoAnnounce(t *testing.T) {
	announce := []string{}
	noAnnounce := []string{"not-a-valid-addr-or-mask"}

	_, err := makeAddrsFactory(announce, noAnnounce)
	assert.Error(t, err)
}

func TestMakeAddrsFactory_CIDRMask(t *testing.T) {
	announce := []string{}
	noAnnounce := []string{"/ip4/10.0.0.0/ipcidr/8"}

	factory, err := makeAddrsFactory(announce, noAnnounce)
	require.NoError(t, err)
	require.NotNil(t, factory)

	inputAddrs := []ma.Multiaddr{
		mustMultiaddr(t, "/ip4/10.0.0.1/tcp/4001"),
		mustMultiaddr(t, "/ip4/192.168.1.1/tcp/4001"),
	}

	result := factory(inputAddrs)

	assert.Len(t, result, 1)
	assert.Equal(t, "/ip4/192.168.1.1/tcp/4001", result[0].String())
}

func TestIpfsStyleAddrsFactory(t *testing.T) {
	announce := []string{"/ip4/1.2.3.4/tcp/4001"}
	noAnnounce := []string{}

	opt := IpfsStyleAddrsFactory(announce, noAnnounce)
	assert.NotNil(t, opt)
}

func TestIpfsStyleAddrsFactory_InvalidAnnounce(t *testing.T) {
	announce := []string{"invalid-addr"}
	noAnnounce := []string{}

	opt := IpfsStyleAddrsFactory(announce, noAnnounce)
	assert.Nil(t, opt)
}

func TestSimpleAddrsFactory(t *testing.T) {
	ctx := context.Background()
	mockNode := testNet(t).Mock(ctx)
	require.NotNil(t, mockNode)
	defer mockNode.Close()

	announce := []string{"/ip4/1.2.3.4/tcp/4001"}
	opt := mockNode.SimpleAddrsFactory(announce, false)
	assert.NotNil(t, opt)
}

func TestSimpleAddrsFactory_Override(t *testing.T) {
	ctx := context.Background()
	mockNode := testNet(t).Mock(ctx)
	require.NotNil(t, mockNode)
	defer mockNode.Close()

	announce := []string{"/ip4/1.2.3.4/tcp/4001"}
	opt := mockNode.SimpleAddrsFactory(announce, true)
	assert.NotNil(t, opt)
}

func TestSimpleAddrsFactory_InvalidAnnounce(t *testing.T) {
	ctx := context.Background()
	mockNode := testNet(t).Mock(ctx)
	require.NotNil(t, mockNode)
	defer mockNode.Close()

	announce := []string{"invalid-addr", "/ip4/1.2.3.4/tcp/4001"}
	opt := mockNode.SimpleAddrsFactory(announce, false)
	assert.NotNil(t, opt)
}

func mustMultiaddr(t *testing.T, s string) ma.Multiaddr {
	t.Helper()
	addr, err := ma.NewMultiaddr(s)
	require.NoError(t, err)
	return addr
}

func TestANodeThatLetTheKernelPickAnnouncesThePortItBound(t *testing.T) {
	unbound := []string{"/ip4/127.0.0.1/tcp/0"}

	node, err := NewPublic(
		t.Context(),
		nil,
		keypair.NewRaw(),
		nil,
		unbound,
		unbound,
		BootstrapParams{},
	)
	require.NoError(t, err)
	defer node.Close()

	addrs := node.Host().Addrs()
	require.NotEmpty(t, addrs, "a node listening on tcp/0 announced nothing")
	for _, addr := range addrs {
		port, err := addr.ValueForProtocol(ma.P_TCP)
		require.NoError(t, err)
		require.NotEqual(t, "0", port, "announced %s, which nothing can dial", addr)
	}

	listening, err := addrs[0].ValueForProtocol(ma.P_TCP)
	require.NoError(t, err)

	// ListenAddresses also carries /p2p-circuit, which names no tcp port.
	var bound string
	for _, addr := range node.Host().Network().ListenAddresses() {
		if port, err := addr.ValueForProtocol(ma.P_TCP); err == nil {
			bound = port
		}
	}
	require.Equal(t, bound, listening, "announced a port other than the one bound")
}

func TestAnAnnouncedPortStillOverridesWhatTheHostBound(t *testing.T) {
	node := testNet(t).Mock(t.Context())
	defer node.Close()

	var cfg config.Config
	require.NoError(t, node.SimpleAddrsFactory([]string{"/ip4/1.2.3.4/tcp/4001"}, true)(&cfg))

	result := cfg.AddrsFactory([]ma.Multiaddr{mustMultiaddr(t, "/ip4/127.0.0.1/tcp/9999")})
	require.Len(t, result, 1)
	require.Equal(t, "/ip4/1.2.3.4/tcp/4001", result[0].String())
}

func TestHasUnboundPort(t *testing.T) {
	for _, addr := range []string{"/ip4/127.0.0.1/tcp/0", "/ip4/127.0.0.1/udp/0/quic-v1"} {
		require.True(t, hasUnboundPort(mustMultiaddr(t, addr)), addr)
	}
	for _, addr := range []string{"/ip4/127.0.0.1/tcp/4001", "/ip4/1.2.3.4/udp/4001/quic-v1"} {
		require.False(t, hasUnboundPort(mustMultiaddr(t, addr)), addr)
	}
}
