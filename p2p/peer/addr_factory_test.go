package peer

import (
	"context"
	"testing"

	ma "github.com/multiformats/go-multiaddr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMakeAddrsFactory_AnnounceOnly(t *testing.T) {
	announce := []string{"/ip4/1.2.3.4/tcp/4001", "/ip4/5.6.7.8/tcp/4001"}
	noAnnounce := []string{}

	factory, err := makeAddrsFactory(announce, noAnnounce)
	require.NoError(t, err)
	require.NotNil(t, factory)

	// Create some input addresses
	inputAddrs := []ma.Multiaddr{
		mustMultiaddr(t, "/ip4/127.0.0.1/tcp/4001"),
		mustMultiaddr(t, "/ip4/192.168.1.1/tcp/4001"),
	}

	result := factory(inputAddrs)

	// Should return announce addresses, not input addresses
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

	// Should return input addresses when no announce addresses
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

	// Should filter out the noAnnounce address
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
	noAnnounce := []string{"/ip4/10.0.0.0/ipcidr/8"} // Block 10.x.x.x

	factory, err := makeAddrsFactory(announce, noAnnounce)
	require.NoError(t, err)
	require.NotNil(t, factory)

	inputAddrs := []ma.Multiaddr{
		mustMultiaddr(t, "/ip4/10.0.0.1/tcp/4001"),
		mustMultiaddr(t, "/ip4/192.168.1.1/tcp/4001"),
	}

	result := factory(inputAddrs)

	// 10.0.0.1 should be filtered out
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
	mockNode := Mock(ctx)
	require.NotNil(t, mockNode)
	defer mockNode.Close()

	announce := []string{"/ip4/1.2.3.4/tcp/4001"}
	opt := mockNode.SimpleAddrsFactory(announce, false)
	assert.NotNil(t, opt)
}

func TestSimpleAddrsFactory_Override(t *testing.T) {
	ctx := context.Background()
	mockNode := Mock(ctx)
	require.NotNil(t, mockNode)
	defer mockNode.Close()

	announce := []string{"/ip4/1.2.3.4/tcp/4001"}
	opt := mockNode.SimpleAddrsFactory(announce, true)
	assert.NotNil(t, opt)
}

func TestSimpleAddrsFactory_InvalidAnnounce(t *testing.T) {
	ctx := context.Background()
	mockNode := Mock(ctx)
	require.NotNil(t, mockNode)
	defer mockNode.Close()

	// Invalid addresses should be skipped
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
