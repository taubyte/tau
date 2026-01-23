package peer

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	peercore "github.com/libp2p/go-libp2p/core/peer"
	keypair "github.com/taubyte/tau/p2p/keypair"
)

// TestPeeringConnectionRefusal tests for connection refusal issues
// This reproduces the production issue where nodes refuse connections
func TestPeeringConnectionRefusal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create multiple peers
	numPeers := 5
	peers := make([]Node, numPeers)
	ports := make([]int, numPeers)

	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	for i := 0; i < numPeers; i++ {
		ports[i] = 12000 + rnd.Intn(20000)
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
	time.Sleep(2 * time.Second)

	// Add all peers to each other's peering service
	// This simulates a mesh network where all peers should connect
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

	// Wait for connections to establish with retries
	// When running with other tests, connections may take longer due to resource contention
	maxWait := 30 * time.Second
	checkInterval := 500 * time.Millisecond
	startTime := time.Now()

	for time.Since(startTime) < maxWait {
		allConnected := true
		for i := 0; i < numPeers; i++ {
			for j := 0; j < numPeers; j++ {
				if i == j {
					continue
				}
				connectedness := peers[i].Peer().Network().Connectedness(peers[j].ID())
				if connectedness.String() != "Connected" {
					allConnected = false
					break
				}
			}
			if !allConnected {
				break
			}
		}
		if allConnected {
			break
		}
		time.Sleep(checkInterval)
	}

	// Verify connections
	connectedCount := 0
	totalConnections := numPeers * (numPeers - 1)
	for i := 0; i < numPeers; i++ {
		for j := 0; j < numPeers; j++ {
			if i == j {
				continue
			}
			connectedness := peers[i].Peer().Network().Connectedness(peers[j].ID())
			if connectedness.String() == "Connected" {
				connectedCount++
			} else {
				t.Logf("Peer %d not connected to peer %d, status: %s", i, j, connectedness)
			}
		}
	}

	connectionRate := float64(connectedCount) / float64(totalConnections) * 100
	if connectionRate < 80 {
		t.Errorf("Connection rate too low: %.2f%% (%d/%d), expected at least 80%%",
			connectionRate, connectedCount, totalConnections)
	}

	// Stress test: repeatedly disconnect and reconnect
	for round := 0; round < 3; round++ {
		// Remove all peers from peering service
		for i := 0; i < numPeers; i++ {
			for j := 0; j < numPeers; j++ {
				if i == j {
					continue
				}
				peers[i].Peering().RemovePeer(peers[j].ID())
			}
		}
		time.Sleep(1 * time.Second)

		// Re-add all peers
		for i := 0; i < numPeers; i++ {
			for j := 0; j < numPeers; j++ {
				if i == j {
					continue
				}
				peers[i].Peering().AddPeer(peercore.AddrInfo{
					ID:    peers[j].ID(),
					Addrs: peers[j].Peer().Addrs(),
				})
			}
		}
		// Wait for reconnection with retries
		reconnectStart := time.Now()
		reconnectMaxWait := 15 * time.Second
		for time.Since(reconnectStart) < reconnectMaxWait {
			allReconnected := true
			for i := 0; i < numPeers; i++ {
				for j := 0; j < numPeers; j++ {
					if i == j {
						continue
					}
					connectedness := peers[i].Peer().Network().Connectedness(peers[j].ID())
					if connectedness.String() != "Connected" {
						allReconnected = false
						break
					}
				}
				if !allReconnected {
					break
				}
			}
			if allReconnected {
				break
			}
			time.Sleep(500 * time.Millisecond)
		}

		// Check connections again
		roundConnectedCount := 0
		for i := 0; i < numPeers; i++ {
			for j := 0; j < numPeers; j++ {
				if i == j {
					continue
				}
				connectedness := peers[i].Peer().Network().Connectedness(peers[j].ID())
				if connectedness.String() == "Connected" {
					roundConnectedCount++
				} else {
					t.Logf("Round %d: Peer %d not connected to peer %d, status: %s", round, i, j, connectedness)
				}
			}
		}

		roundConnectionRate := float64(roundConnectedCount) / float64(totalConnections) * 100
		if roundConnectionRate < 70 {
			t.Errorf("Round %d: Connection rate too low: %.2f%% (%d/%d), expected at least 70%%",
				round, roundConnectionRate, roundConnectedCount, totalConnections)
		}
	}
}

// TestPeeringLongNegotiation tests for long negotiation times
// This reproduces the production issue where negotiation takes a long time
func TestPeeringLongNegotiation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create two peers
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	port1 := 13000 + rnd.Intn(20000)
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

	// Wait for peers to be ready
	time.Sleep(2 * time.Second)

	// Measure connection time
	startTime := time.Now()
	p1.Peering().AddPeer(peercore.AddrInfo{
		ID:    p2.ID(),
		Addrs: p2.Peer().Addrs(),
	})

	// Wait for connection with timeout
	timeout := 30 * time.Second
	connected := false
	for time.Since(startTime) < timeout {
		if p1.Peer().Network().Connectedness(p2.ID()).String() == "Connected" {
			connected = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !connected {
		t.Fatalf("Connection not established within %v", timeout)
	}

	connectionTime := time.Since(startTime)
	if connectionTime > 10*time.Second {
		t.Errorf("Connection took too long: %v (expected < 10s)", connectionTime)
	}

	// Test multiple simultaneous connection attempts
	var wg sync.WaitGroup
	attempts := 10
	successCount := 0
	var mu sync.Mutex

	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start := time.Now()
			p1.Peering().RemovePeer(p2.ID())
			time.Sleep(500 * time.Millisecond)
			p1.Peering().AddPeer(peercore.AddrInfo{
				ID:    p2.ID(),
				Addrs: p2.Peer().Addrs(),
			})

			// Wait for reconnection
			for time.Since(start) < 15*time.Second {
				if p1.Peer().Network().Connectedness(p2.ID()).String() == "Connected" {
					mu.Lock()
					successCount++
					mu.Unlock()
					return
				}
				time.Sleep(100 * time.Millisecond)
			}
		}()
	}

	wg.Wait()
	time.Sleep(2 * time.Second)

	if successCount < attempts {
		t.Errorf("Only %d/%d reconnection attempts succeeded", successCount, attempts)
	}
}

// TestPeeringRaceCondition tests for race conditions in connection management
func TestPeeringRaceCondition(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dir1 := t.TempDir()
	dir2 := t.TempDir()

	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	port1 := 14000 + rnd.Intn(20000)
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

	// Race condition: rapidly add/remove peer while connection is being established
	var wg sync.WaitGroup
	iterations := 20

	for i := 0; i < iterations; i++ {
		wg.Add(1)
		go func(iter int) {
			defer wg.Done()
			// Add peer
			p1.Peering().AddPeer(peercore.AddrInfo{
				ID:    p2.ID(),
				Addrs: p2.Peer().Addrs(),
			})
			time.Sleep(time.Duration(rand.Intn(100)) * time.Millisecond)
			// Remove peer
			p1.Peering().RemovePeer(p2.ID())
			time.Sleep(time.Duration(rand.Intn(100)) * time.Millisecond)
		}(i)
	}

	wg.Wait()
	time.Sleep(2 * time.Second)

	// Final state: add peer and verify connection
	p1.Peering().AddPeer(peercore.AddrInfo{
		ID:    p2.ID(),
		Addrs: p2.Peer().Addrs(),
	})

	timeout := 10 * time.Second
	start := time.Now()
	for time.Since(start) < timeout {
		if p1.Peer().Network().Connectedness(p2.ID()).String() == "Connected" {
			return // Success
		}
		time.Sleep(100 * time.Millisecond)
	}

	t.Errorf("Connection not established after race condition test")
}
