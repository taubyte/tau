package helpers

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/p2p/datastores/mem"
	"github.com/taubyte/tau/p2p/keypair"

	libp2pcrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

func TestLibp2pOptionsBase(t *testing.T) {
	assert.NotNil(t, Libp2pOptionsBase)
	assert.True(t, len(Libp2pOptionsBase) > 0)
}

func TestLibp2pOptionsFullNode(t *testing.T) {
	assert.NotNil(t, Libp2pOptionsFullNode)
	assert.True(t, len(Libp2pOptionsFullNode) > 0)
}

func TestLibp2pOptionsPublicNode(t *testing.T) {
	assert.NotNil(t, Libp2pOptionsPublicNode)
	assert.True(t, len(Libp2pOptionsPublicNode) > 0)
}

func TestLibp2pOptionsLitePublicNode(t *testing.T) {
	assert.NotNil(t, Libp2pOptionsLitePublicNode)
	assert.True(t, len(Libp2pOptionsLitePublicNode) > 0)
}

func TestLibp2pSimpleNodeOptions(t *testing.T) {
	assert.NotNil(t, Libp2pSimpleNodeOptions)
	assert.True(t, len(Libp2pSimpleNodeOptions) > 0)
}

func TestLibp2pLitePrivateNodeOptions(t *testing.T) {
	assert.NotNil(t, Libp2pLitePrivateNodeOptions)
	assert.True(t, len(Libp2pLitePrivateNodeOptions) > 0)
}

func TestDefaultValues(t *testing.T) {
	assert.Equal(t, 400, DefaultConnMgrHighWater)
	assert.Equal(t, 100, DefaultConnMgrLowWater)
	assert.Equal(t, 2*time.Minute, DefaultConnMgrGracePeriod)
	assert.Equal(t, 3*time.Second, DefaultDialPeerTimeout)
}

func TestSetupLibp2p(t *testing.T) {
	ctx := context.Background()

	// Create a private key
	rawKey := keypair.NewRaw()
	privKey, err := libp2pcrypto.UnmarshalPrivateKey(rawKey)
	require.NoError(t, err)

	// Create an in-memory datastore
	ds := mem.New()
	defer ds.Close()

	// Setup libp2p with minimal options
	host, routing, err := SetupLibp2p(
		ctx,
		privKey,
		nil, // no swarm key
		[]string{"/ip4/127.0.0.1/tcp/0"},
		ds,
		nil, // no bootstrap peers
		Libp2pSimpleNodeOptions...,
	)
	require.NoError(t, err)
	require.NotNil(t, host)
	require.NotNil(t, routing)

	defer host.Close()

	// Verify host is working
	assert.NotEmpty(t, host.ID())
	assert.True(t, len(host.Addrs()) > 0)
}

func TestBootstrap_EmptyPeers(t *testing.T) {
	ctx := context.Background()

	rawKey := keypair.NewRaw()
	privKey, err := libp2pcrypto.UnmarshalPrivateKey(rawKey)
	require.NoError(t, err)

	ds := mem.New()
	defer ds.Close()

	host, routing, err := SetupLibp2p(
		ctx,
		privKey,
		nil,
		[]string{"/ip4/127.0.0.1/tcp/0"},
		ds,
		nil,
		Libp2pSimpleNodeOptions...,
	)
	require.NoError(t, err)
	defer host.Close()

	// Bootstrap with empty peer list
	result, err := Bootstrap(ctx, host, routing, []peer.AddrInfo{})
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestSetupLibp2p_WithBootstrapFunc(t *testing.T) {
	ctx := context.Background()

	rawKey := keypair.NewRaw()
	privKey, err := libp2pcrypto.UnmarshalPrivateKey(rawKey)
	require.NoError(t, err)

	ds := mem.New()
	defer ds.Close()

	bootstrapPeers := func() []peer.AddrInfo {
		return nil
	}

	host, routing, err := SetupLibp2p(
		ctx,
		privKey,
		nil,
		[]string{"/ip4/127.0.0.1/tcp/0"},
		ds,
		bootstrapPeers,
		Libp2pSimpleNodeOptions...,
	)
	require.NoError(t, err)
	require.NotNil(t, host)
	require.NotNil(t, routing)

	defer host.Close()
}

func TestBootstrap_TwoPeers(t *testing.T) {
	ctx := context.Background()

	// Create first peer
	rawKey1 := keypair.NewRaw()
	privKey1, err := libp2pcrypto.UnmarshalPrivateKey(rawKey1)
	require.NoError(t, err)

	ds1 := mem.New()
	defer ds1.Close()

	host1, routing1, err := SetupLibp2p(
		ctx,
		privKey1,
		nil,
		[]string{"/ip4/127.0.0.1/tcp/0"},
		ds1,
		nil,
		Libp2pSimpleNodeOptions...,
	)
	require.NoError(t, err)
	defer host1.Close()

	// Create second peer
	rawKey2 := keypair.NewRaw()
	privKey2, err := libp2pcrypto.UnmarshalPrivateKey(rawKey2)
	require.NoError(t, err)

	ds2 := mem.New()
	defer ds2.Close()

	host2, routing2, err := SetupLibp2p(
		ctx,
		privKey2,
		nil,
		[]string{"/ip4/127.0.0.1/tcp/0"},
		ds2,
		nil,
		Libp2pSimpleNodeOptions...,
	)
	require.NoError(t, err)
	defer host2.Close()

	// Bootstrap peer2 with peer1
	peers := []peer.AddrInfo{
		{ID: host1.ID(), Addrs: host1.Addrs()},
	}

	result, err := Bootstrap(ctx, host2, routing2, peers)
	require.NoError(t, err)
	assert.Len(t, result, 1)

	// Also bootstrap routing1 just to ensure DHT is up
	err = routing1.Bootstrap(ctx)
	require.NoError(t, err)
}

func TestSetupLibp2p_MultipleAddrs(t *testing.T) {
	ctx := context.Background()

	rawKey := keypair.NewRaw()
	privKey, err := libp2pcrypto.UnmarshalPrivateKey(rawKey)
	require.NoError(t, err)

	ds := mem.New()
	defer ds.Close()

	host, routing, err := SetupLibp2p(
		ctx,
		privKey,
		nil,
		[]string{
			"/ip4/127.0.0.1/tcp/0",
			"/ip4/127.0.0.1/udp/0/quic-v1",
		},
		ds,
		nil,
		Libp2pSimpleNodeOptions...,
	)
	require.NoError(t, err)
	require.NotNil(t, host)
	require.NotNil(t, routing)

	defer host.Close()

	// Should have multiple addresses
	assert.True(t, len(host.Addrs()) >= 2)
}

func TestBootstrap_WithPeers(t *testing.T) {
	ctx := context.Background()

	// Create first peer
	rawKey1 := keypair.NewRaw()
	privKey1, err := libp2pcrypto.UnmarshalPrivateKey(rawKey1)
	require.NoError(t, err)

	ds1 := mem.New()
	defer ds1.Close()

	host1, _, err := SetupLibp2p(
		ctx,
		privKey1,
		nil,
		[]string{"/ip4/127.0.0.1/tcp/0"},
		ds1,
		nil,
		Libp2pSimpleNodeOptions...,
	)
	require.NoError(t, err)
	defer host1.Close()

	// Create second peer
	rawKey2 := keypair.NewRaw()
	privKey2, err := libp2pcrypto.UnmarshalPrivateKey(rawKey2)
	require.NoError(t, err)

	ds2 := mem.New()
	defer ds2.Close()

	host2, routing2, err := SetupLibp2p(
		ctx,
		privKey2,
		nil,
		[]string{"/ip4/127.0.0.1/tcp/0"},
		ds2,
		nil,
		Libp2pSimpleNodeOptions...,
	)
	require.NoError(t, err)
	defer host2.Close()

	// Create third peer and connect it to both
	rawKey3 := keypair.NewRaw()
	privKey3, err := libp2pcrypto.UnmarshalPrivateKey(rawKey3)
	require.NoError(t, err)

	ds3 := mem.New()
	defer ds3.Close()

	host3, routing3, err := SetupLibp2p(
		ctx,
		privKey3,
		nil,
		[]string{"/ip4/127.0.0.1/tcp/0"},
		ds3,
		nil,
		Libp2pSimpleNodeOptions...,
	)
	require.NoError(t, err)
	defer host3.Close()

	// Connect peer3 to peer1 and peer2
	peers := []peer.AddrInfo{
		{ID: host1.ID(), Addrs: host1.Addrs()},
		{ID: host2.ID(), Addrs: host2.Addrs()},
	}

	result, err := Bootstrap(ctx, host3, routing3, peers)
	require.NoError(t, err)
	assert.Len(t, result, 2)

	// Bootstrap routing2 as well
	err = routing2.Bootstrap(ctx)
	require.NoError(t, err)
}

func TestSetupLibp2p_FullNodeOptions(t *testing.T) {
	ctx := context.Background()

	rawKey := keypair.NewRaw()
	privKey, err := libp2pcrypto.UnmarshalPrivateKey(rawKey)
	require.NoError(t, err)

	ds := mem.New()
	defer ds.Close()

	host, routing, err := SetupLibp2p(
		ctx,
		privKey,
		nil,
		[]string{"/ip4/127.0.0.1/tcp/0"},
		ds,
		nil,
		Libp2pOptionsFullNode...,
	)
	require.NoError(t, err)
	require.NotNil(t, host)
	require.NotNil(t, routing)

	defer host.Close()
}

func TestSetupLibp2p_PublicNodeOptions(t *testing.T) {
	ctx := context.Background()

	rawKey := keypair.NewRaw()
	privKey, err := libp2pcrypto.UnmarshalPrivateKey(rawKey)
	require.NoError(t, err)

	ds := mem.New()
	defer ds.Close()

	host, routing, err := SetupLibp2p(
		ctx,
		privKey,
		nil,
		[]string{"/ip4/127.0.0.1/tcp/0"},
		ds,
		nil,
		Libp2pOptionsPublicNode...,
	)
	require.NoError(t, err)
	require.NotNil(t, host)
	require.NotNil(t, routing)

	defer host.Close()
}

func TestSetupLibp2p_LitePublicNodeOptions(t *testing.T) {
	ctx := context.Background()

	rawKey := keypair.NewRaw()
	privKey, err := libp2pcrypto.UnmarshalPrivateKey(rawKey)
	require.NoError(t, err)

	ds := mem.New()
	defer ds.Close()

	host, routing, err := SetupLibp2p(
		ctx,
		privKey,
		nil,
		[]string{"/ip4/127.0.0.1/tcp/0"},
		ds,
		nil,
		Libp2pOptionsLitePublicNode...,
	)
	require.NoError(t, err)
	require.NotNil(t, host)
	require.NotNil(t, routing)

	defer host.Close()
}

func TestSetupLibp2p_LitePrivateNodeOptions(t *testing.T) {
	ctx := context.Background()

	rawKey := keypair.NewRaw()
	privKey, err := libp2pcrypto.UnmarshalPrivateKey(rawKey)
	require.NoError(t, err)

	ds := mem.New()
	defer ds.Close()

	host, routing, err := SetupLibp2p(
		ctx,
		privKey,
		nil,
		[]string{"/ip4/127.0.0.1/tcp/0"},
		ds,
		nil,
		Libp2pLitePrivateNodeOptions...,
	)
	require.NoError(t, err)
	require.NotNil(t, host)
	require.NotNil(t, routing)

	defer host.Close()
}
