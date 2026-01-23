package peer

import (
	"testing"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/multiformats/go-multiaddr"
)

// TestNetNotifeeStubMethods tests the no-op stub methods of netNotifee
// These methods are required by the network.Notifiee interface but don't do anything
func TestNetNotifeeStubMethods(t *testing.T) {
	// Create a minimal peeringService for testing
	ps := &peeringService{}
	nn := (*netNotifee)(ps)

	// These methods should not panic and should do nothing
	t.Run("OpenedStream", func(t *testing.T) {
		nn.OpenedStream(nil, nil)
	})

	t.Run("ClosedStream", func(t *testing.T) {
		nn.ClosedStream(nil, nil)
	})

	t.Run("Listen", func(t *testing.T) {
		nn.Listen(nil, nil)
	})

	t.Run("ListenClose", func(t *testing.T) {
		nn.ListenClose(nil, nil)
	})
}

// TestNetNotifeeInterface ensures netNotifee implements network.Notifiee
func TestNetNotifeeInterface(t *testing.T) {
	ps := &peeringService{}
	nn := (*netNotifee)(ps)

	// This is a compile-time check that netNotifee implements network.Notifiee
	var _ network.Notifiee = nn
}

// TestNetNotifeeStubMethodsWithArgs tests stub methods with non-nil arguments
func TestNetNotifeeStubMethodsWithArgs(t *testing.T) {
	ps := &peeringService{}
	nn := (*netNotifee)(ps)

	// Create a mock multiaddr for testing
	ma, err := multiaddr.NewMultiaddr("/ip4/127.0.0.1/tcp/4001")
	if err != nil {
		t.Fatalf("Failed to create multiaddr: %v", err)
	}

	// These should not panic even with valid multiaddr
	nn.Listen(nil, ma)
	nn.ListenClose(nil, ma)
}
