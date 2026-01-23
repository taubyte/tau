//go:build stress

package peer

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	peercore "github.com/libp2p/go-libp2p/core/peer"
	keypair "github.com/taubyte/tau/p2p/keypair"
)

// TestPeeringStressRapidConnectDisconnect tests rapid connect/disconnect cycles
func TestPeeringStressRapidConnectDisconnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create two peers
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	port1 := 20000 + rnd.Intn(20000)
	port2 := port1 + 1

	p1, err := New(
		ctx,
		dir1,
		keypair.NewRaw(),
		nil,
		[]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", port1)},
		nil,
		true,
		false,
	)
	if err != nil {
		t.Fatalf("Failed to create peer 1: %v", err)
	}
	defer p1.Close()

	p2, err := New(
		ctx,
		dir2,
		keypair.NewRaw(),
		nil,
		[]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", port2)},
		nil,
		true,
		false,
	)
	if err != nil {
		t.Fatalf("Failed to create peer 2: %v", err)
	}
	defer p2.Close()

	time.Sleep(2 * time.Second)

	// Rapidly add/remove peer 100 times
	iterations := 100
	var successCount int64
	var wg sync.WaitGroup

	for i := 0; i < iterations; i++ {
		wg.Add(1)
		go func(iter int) {
			defer wg.Done()

			// Add peer
			p1.Peering().AddPeer(peercore.AddrInfo{
				ID:    p2.ID(),
				Addrs: p2.Peer().Addrs(),
			})

			// Wait for connection attempt (connections may take time)
			time.Sleep(time.Duration(100+rand.Intn(100)) * time.Millisecond)

			// Check if connected
			if p1.Peer().Network().Connectedness(p2.ID()).String() == "Connected" {
				atomic.AddInt64(&successCount, 1)
			}

			// Remove peer
			p1.Peering().RemovePeer(p2.ID())

			// Small delay before next iteration
			time.Sleep(time.Duration(rand.Intn(50)) * time.Millisecond)
		}(i)
	}

	wg.Wait()
	time.Sleep(2 * time.Second)

	// Final connection attempt
	p1.Peering().AddPeer(peercore.AddrInfo{
		ID:    p2.ID(),
		Addrs: p2.Peer().Addrs(),
	})

	// Wait for final connection
	timeout := 10 * time.Second
	start := time.Now()
	for time.Since(start) < timeout {
		if p1.Peer().Network().Connectedness(p2.ID()).String() == "Connected" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	finalConnected := p1.Peer().Network().Connectedness(p2.ID()).String() == "Connected"

	successRate := float64(successCount) / float64(iterations) * 100
	t.Logf("Success rate during rapid cycles: %.2f%% (%d/%d)", successRate, successCount, iterations)
	t.Logf("Final connection state: %v", finalConnected)

	if !finalConnected {
		t.Errorf("Final connection not established after stress test")
	}
	// During rapid cycles, connections may not establish immediately, but final connection should work
	// Lower threshold to account for timing in rapid cycles
	if successRate < 20 {
		t.Errorf("Success rate too low: %.2f%%, expected at least 20%% (rapid cycles may prevent immediate connections)", successRate)
	}
}

// TestPeeringStressManyPeers tests many peers connecting simultaneously
func TestPeeringStressManyPeers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	numPeers := 10
	peers := make([]Node, numPeers)
	ports := make([]int, numPeers)

	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	for i := 0; i < numPeers; i++ {
		ports[i] = 21000 + rnd.Intn(20000)
		dir := t.TempDir()
		p, err := New(
			ctx,
			dir,
			keypair.NewRaw(),
			nil,
			[]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", ports[i])},
			nil,
			true,
			false,
		)
		if err != nil {
			t.Fatalf("Failed to create peer %d: %v", i, err)
		}
		peers[i] = p
		defer p.Close()
	}

	// Wait for peers to be ready
	time.Sleep(3 * time.Second)

	// Create a mesh: each peer connects to all others
	var wg sync.WaitGroup
	for i := 0; i < numPeers; i++ {
		for j := 0; j < numPeers; j++ {
			if i == j {
				continue
			}
			wg.Add(1)
			go func(from, to int) {
				defer wg.Done()
				peers[from].Peering().AddPeer(peercore.AddrInfo{
					ID:    peers[to].ID(),
					Addrs: peers[to].Peer().Addrs(),
				})
			}(i, j)
		}
	}
	wg.Wait()

	// Wait for connections to establish
	time.Sleep(10 * time.Second)

	// Verify all connections
	connectedCount := 0
	totalConnections := numPeers * (numPeers - 1)

	for i := 0; i < numPeers; i++ {
		for j := 0; j < numPeers; j++ {
			if i == j {
				continue
			}
			if peers[i].Peer().Network().Connectedness(peers[j].ID()).String() == "Connected" {
				connectedCount++
			}
		}
	}

	connectionRate := float64(connectedCount) / float64(totalConnections) * 100
	t.Logf("Connection rate: %.2f%% (%d/%d)", connectionRate, connectedCount, totalConnections)

	if connectionRate < 80 {
		t.Errorf("Connection rate too low: %.2f%%, expected at least 80%%", connectionRate)
	}
}

// TestPeeringStressConnectionManager tests connection manager behavior under stress
func TestPeeringStressConnectionManager(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create many peers to stress the connection manager
	numPeers := 15
	peers := make([]Node, numPeers)
	ports := make([]int, numPeers)

	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	for i := 0; i < numPeers; i++ {
		ports[i] = 22000 + rnd.Intn(20000)
		dir := t.TempDir()
		p, err := New(
			ctx,
			dir,
			keypair.NewRaw(),
			nil,
			[]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", ports[i])},
			nil,
			true,
			false,
		)
		if err != nil {
			t.Fatalf("Failed to create peer %d: %v", i, err)
		}
		peers[i] = p
		defer p.Close()
	}

	time.Sleep(3 * time.Second)

	// Connect each peer to a subset of others (to avoid too many connections)
	connectionsPerPeer := 5
	var wg sync.WaitGroup
	for i := 0; i < numPeers; i++ {
		for j := 0; j < connectionsPerPeer; j++ {
			target := (i + j + 1) % numPeers
			if target == i {
				target = (target + 1) % numPeers
			}
			wg.Add(1)
			go func(from, to int) {
				defer wg.Done()
				peers[from].Peering().AddPeer(peercore.AddrInfo{
					ID:    peers[to].ID(),
					Addrs: peers[to].Peer().Addrs(),
				})
			}(i, target)
		}
	}
	wg.Wait()

	// Wait for connections
	time.Sleep(8 * time.Second)

	// Verify protected connections are maintained
	stableCount := 0
	unstableCount := 0

	for i := 0; i < numPeers; i++ {
		for j := 0; j < connectionsPerPeer; j++ {
			target := (i + j + 1) % numPeers
			if target == i {
				target = (target + 1) % numPeers
			}
			connectedness := peers[i].Peer().Network().Connectedness(peers[target].ID())
			if connectedness.String() == "Connected" {
				stableCount++
			} else {
				unstableCount++
			}
		}
	}

	totalExpected := numPeers * connectionsPerPeer
	stabilityRate := float64(stableCount) / float64(totalExpected) * 100
	t.Logf("Connection stability: %.2f%% (%d stable, %d unstable out of %d expected)",
		stabilityRate, stableCount, unstableCount, totalExpected)

	if stabilityRate < 70 {
		t.Errorf("Connection stability too low: %.2f%%, expected at least 70%%", stabilityRate)
	}
}

// TestPeeringStressSimultaneousAddRemove tests simultaneous add/remove operations
func TestPeeringStressSimultaneousAddRemove(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dir1 := t.TempDir()
	dir2 := t.TempDir()

	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	port1 := 23000 + rnd.Intn(20000)
	port2 := port1 + 1

	p1, err := New(
		ctx,
		dir1,
		keypair.NewRaw(),
		nil,
		[]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", port1)},
		nil,
		true,
		false,
	)
	if err != nil {
		t.Fatalf("Failed to create peer 1: %v", err)
	}
	defer p1.Close()

	p2, err := New(
		ctx,
		dir2,
		keypair.NewRaw(),
		nil,
		[]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", port2)},
		nil,
		true,
		false,
	)
	if err != nil {
		t.Fatalf("Failed to create peer 2: %v", err)
	}
	defer p2.Close()

	time.Sleep(2 * time.Second)

	// Run add/remove operations from both sides simultaneously
	iterations := 50
	var wg sync.WaitGroup
	var errors int64

	for i := 0; i < iterations; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			p1.Peering().AddPeer(peercore.AddrInfo{
				ID:    p2.ID(),
				Addrs: p2.Peer().Addrs(),
			})
			time.Sleep(time.Duration(rand.Intn(20)) * time.Millisecond)
			p1.Peering().RemovePeer(p2.ID())
		}()
		go func() {
			defer wg.Done()
			p2.Peering().AddPeer(peercore.AddrInfo{
				ID:    p1.ID(),
				Addrs: p1.Peer().Addrs(),
			})
			time.Sleep(time.Duration(rand.Intn(20)) * time.Millisecond)
			p2.Peering().RemovePeer(p1.ID())
		}()
	}

	wg.Wait()
	time.Sleep(2 * time.Second)

	// Final state: both should be able to connect
	p1.Peering().AddPeer(peercore.AddrInfo{
		ID:    p2.ID(),
		Addrs: p2.Peer().Addrs(),
	})
	p2.Peering().AddPeer(peercore.AddrInfo{
		ID:    p1.ID(),
		Addrs: p1.Peer().Addrs(),
	})

	timeout := 10 * time.Second
	start := time.Now()
	bothConnected := false
	for time.Since(start) < timeout {
		p1Conn := p1.Peer().Network().Connectedness(p2.ID()).String() == "Connected"
		p2Conn := p2.Peer().Network().Connectedness(p1.ID()).String() == "Connected"
		if p1Conn && p2Conn {
			bothConnected = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !bothConnected {
		t.Errorf("Final connection not established after simultaneous operations (errors: %d)", errors)
	}
}

// TestPeeringStressBackoffRecovery tests that backoff doesn't prevent eventual connection
func TestPeeringStressBackoffRecovery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dir1 := t.TempDir()
	dir2 := t.TempDir()

	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	port1 := 24000 + rnd.Intn(20000)
	port2 := port1 + 1

	p1, err := New(
		ctx,
		dir1,
		keypair.NewRaw(),
		nil,
		[]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", port1)},
		nil,
		true,
		false,
	)
	if err != nil {
		t.Fatalf("Failed to create peer 1: %v", err)
	}
	defer p1.Close()

	p2, err := New(
		ctx,
		dir2,
		keypair.NewRaw(),
		nil,
		[]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", port2)},
		nil,
		true,
		false,
	)
	if err != nil {
		t.Fatalf("Failed to create peer 2: %v", err)
	}
	defer p2.Close()

	time.Sleep(2 * time.Second)

	// Add peer (should connect immediately now)
	p1.Peering().AddPeer(peercore.AddrInfo{
		ID:    p2.ID(),
		Addrs: p2.Peer().Addrs(),
	})

	// Wait for connection
	time.Sleep(2 * time.Second)

	// Verify connected
	if p1.Peer().Network().Connectedness(p2.ID()).String() != "Connected" {
		t.Fatalf("Initial connection not established")
	}

	// Force disconnect by removing and re-adding multiple times
	for i := 0; i < 5; i++ {
		p1.Peering().RemovePeer(p2.ID())
		time.Sleep(500 * time.Millisecond)
		p1.Peering().AddPeer(peercore.AddrInfo{
			ID:    p2.ID(),
			Addrs: p2.Peer().Addrs(),
		})
		time.Sleep(1 * time.Second)
	}

	// Final check - should be connected despite backoff
	timeout := 15 * time.Second
	start := time.Now()
	connected := false
	for time.Since(start) < timeout {
		if p1.Peer().Network().Connectedness(p2.ID()).String() == "Connected" {
			connected = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	if !connected {
		t.Errorf("Connection not recovered after backoff cycles")
	}
}

// TestPeeringStressConnectDisconnectReconnect tests the full connect->disconnect->reconnect cycle
// This specifically validates that connections can be reliably re-established after disconnection
func TestPeeringStressConnectDisconnectReconnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create two peers
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	port1 := 28000 + rnd.Intn(20000)
	port2 := port1 + 1

	p1, err := New(
		ctx,
		dir1,
		keypair.NewRaw(),
		nil,
		[]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", port1)},
		nil,
		true,
		false,
	)
	if err != nil {
		t.Fatalf("Failed to create peer 1: %v", err)
	}
	defer p1.Close()

	p2, err := New(
		ctx,
		dir2,
		keypair.NewRaw(),
		nil,
		[]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", port2)},
		nil,
		true,
		false,
	)
	if err != nil {
		t.Fatalf("Failed to create peer 2: %v", err)
	}
	defer p2.Close()

	time.Sleep(2 * time.Second)

	// Test multiple connect->disconnect->reconnect cycles
	cycles := 20
	successfulCycles := 0
	failedCycles := 0

	for cycle := 0; cycle < cycles; cycle++ {
		// Step 1: Connect
		p1.Peering().AddPeer(peercore.AddrInfo{
			ID:    p2.ID(),
			Addrs: p2.Peer().Addrs(),
		})

		// Wait for connection with timeout
		connected := false
		connectTimeout := 10 * time.Second
		connectStart := time.Now()
		for time.Since(connectStart) < connectTimeout {
			if p1.Peer().Network().Connectedness(p2.ID()).String() == "Connected" {
				connected = true
				break
			}
			time.Sleep(100 * time.Millisecond)
		}

		if !connected {
			t.Logf("Cycle %d: Failed to connect within %v", cycle, connectTimeout)
			failedCycles++
			// Still try to disconnect and continue
			p1.Peering().RemovePeer(p2.ID())
			time.Sleep(500 * time.Millisecond)
			continue
		}

		// Step 2: Verify connected state
		if p1.Peer().Network().Connectedness(p2.ID()).String() != "Connected" {
			t.Logf("Cycle %d: Connection lost immediately after establishment", cycle)
			failedCycles++
			p1.Peering().RemovePeer(p2.ID())
			time.Sleep(500 * time.Millisecond)
			continue
		}

		// Step 3: Disconnect
		p1.Peering().RemovePeer(p2.ID())

		// Wait for disconnection
		disconnected := false
		disconnectTimeout := 5 * time.Second
		disconnectStart := time.Now()
		for time.Since(disconnectStart) < disconnectTimeout {
			connState := p1.Peer().Network().Connectedness(p2.ID()).String()
			if connState != "Connected" {
				disconnected = true
				break
			}
			time.Sleep(100 * time.Millisecond)
		}

		if !disconnected {
			t.Logf("Cycle %d: Failed to disconnect within %v", cycle, disconnectTimeout)
			// Connection might still be there, try to remove again and continue
			time.Sleep(500 * time.Millisecond)
		}

		// Step 4: Reconnect
		p1.Peering().AddPeer(peercore.AddrInfo{
			ID:    p2.ID(),
			Addrs: p2.Peer().Addrs(),
		})

		// Wait for reconnection
		reconnected := false
		reconnectTimeout := 10 * time.Second
		reconnectStart := time.Now()
		for time.Since(reconnectStart) < reconnectTimeout {
			if p1.Peer().Network().Connectedness(p2.ID()).String() == "Connected" {
				reconnected = true
				break
			}
			time.Sleep(100 * time.Millisecond)
		}

		if reconnected {
			successfulCycles++
			t.Logf("Cycle %d: Successfully completed connect->disconnect->reconnect", cycle)
		} else {
			t.Logf("Cycle %d: Failed to reconnect within %v", cycle, reconnectTimeout)
			failedCycles++
		}

		// Small delay between cycles
		time.Sleep(200 * time.Millisecond)
	}

	successRate := float64(successfulCycles) / float64(cycles) * 100
	t.Logf("Connect->Disconnect->Reconnect success rate: %.2f%% (%d/%d successful, %d failed)",
		successRate, successfulCycles, cycles, failedCycles)

	// Should succeed in at least 80% of cycles
	if successRate < 80 {
		t.Errorf("Success rate too low: %.2f%%, expected at least 80%%", successRate)
	}

	// Final verification: should be able to connect one more time
	p1.Peering().AddPeer(peercore.AddrInfo{
		ID:    p2.ID(),
		Addrs: p2.Peer().Addrs(),
	})

	finalTimeout := 10 * time.Second
	finalStart := time.Now()
	finalConnected := false
	for time.Since(finalStart) < finalTimeout {
		if p1.Peer().Network().Connectedness(p2.ID()).String() == "Connected" {
			finalConnected = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !finalConnected {
		t.Errorf("Final connection not established after all cycles")
	}
}

// TestPeeringStressBidirectionalReconnect tests reconnect from both sides simultaneously
func TestPeeringStressBidirectionalReconnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dir1 := t.TempDir()
	dir2 := t.TempDir()

	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	port1 := 29000 + rnd.Intn(20000)
	port2 := port1 + 1

	p1, err := New(
		ctx,
		dir1,
		keypair.NewRaw(),
		nil,
		[]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", port1)},
		nil,
		true,
		false,
	)
	if err != nil {
		t.Fatalf("Failed to create peer 1: %v", err)
	}
	defer p1.Close()

	p2, err := New(
		ctx,
		dir2,
		keypair.NewRaw(),
		nil,
		[]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", port2)},
		nil,
		true,
		false,
	)
	if err != nil {
		t.Fatalf("Failed to create peer 2: %v", err)
	}
	defer p2.Close()

	time.Sleep(2 * time.Second)

	// Both peers add each other
	p1.Peering().AddPeer(peercore.AddrInfo{
		ID:    p2.ID(),
		Addrs: p2.Peer().Addrs(),
	})
	p2.Peering().AddPeer(peercore.AddrInfo{
		ID:    p1.ID(),
		Addrs: p1.Peer().Addrs(),
	})

	// Wait for initial connection
	timeout := 10 * time.Second
	start := time.Now()
	for time.Since(start) < timeout {
		p1Conn := p1.Peer().Network().Connectedness(p2.ID()).String() == "Connected"
		p2Conn := p2.Peer().Network().Connectedness(p1.ID()).String() == "Connected"
		if p1Conn && p2Conn {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Test multiple disconnect/reconnect cycles from both sides
	cycles := 15
	successfulCycles := 0

	for cycle := 0; cycle < cycles; cycle++ {
		// Disconnect from both sides
		p1.Peering().RemovePeer(p2.ID())
		p2.Peering().RemovePeer(p1.ID())

		// Wait for disconnection
		time.Sleep(1 * time.Second)

		// Reconnect from both sides simultaneously
		p1.Peering().AddPeer(peercore.AddrInfo{
			ID:    p2.ID(),
			Addrs: p2.Peer().Addrs(),
		})
		p2.Peering().AddPeer(peercore.AddrInfo{
			ID:    p1.ID(),
			Addrs: p1.Peer().Addrs(),
		})

		// Wait for reconnection
		reconnectTimeout := 10 * time.Second
		reconnectStart := time.Now()
		bothReconnected := false
		for time.Since(reconnectStart) < reconnectTimeout {
			p1Conn := p1.Peer().Network().Connectedness(p2.ID()).String() == "Connected"
			p2Conn := p2.Peer().Network().Connectedness(p1.ID()).String() == "Connected"
			if p1Conn && p2Conn {
				bothReconnected = true
				successfulCycles++
				break
			}
			time.Sleep(100 * time.Millisecond)
		}

		if !bothReconnected {
			t.Logf("Cycle %d: Failed to reconnect from both sides", cycle)
		}

		time.Sleep(300 * time.Millisecond)
	}

	successRate := float64(successfulCycles) / float64(cycles) * 100
	t.Logf("Bidirectional reconnect success rate: %.2f%% (%d/%d)", successRate, successfulCycles, cycles)

	if successRate < 80 {
		t.Errorf("Bidirectional reconnect success rate too low: %.2f%%, expected at least 80%%", successRate)
	}
}
