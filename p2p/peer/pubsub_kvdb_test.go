package peer

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	peercore "github.com/libp2p/go-libp2p/core/peer"
	keypair "github.com/taubyte/tau/p2p/keypair"
)

// TestPubSubShutdownKVDBDesync tests for pubsub shutdown causing kvdb desync
// This reproduces the production issue where pubsub connections shutdown cause kvdb desync
func TestPubSubShutdownKVDBDesync(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create two peers
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	port1 := 15000 + rnd.Intn(20000)
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

	// Connect peers
	time.Sleep(2 * time.Second)
	p1.Peering().AddPeer(peercore.AddrInfo{
		ID:    p2.ID(),
		Addrs: p2.Peer().Addrs(),
	})
	p2.Peering().AddPeer(peercore.AddrInfo{
		ID:    p1.ID(),
		Addrs: p1.Peer().Addrs(),
	})

	// Wait for connection
	timeout := 10 * time.Second
	start := time.Now()
	for time.Since(start) < timeout {
		if p1.Peer().Network().Connectedness(p2.ID()).String() == "Connected" &&
			p2.Peer().Network().Connectedness(p1.ID()).String() == "Connected" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Subscribe to a topic on both peers
	topicName := "test-kvdb-topic"
	var p1Messages, p2Messages []string
	var p1Mu, p2Mu sync.Mutex
	var p1Err, p2Err error

	// Peer 1 subscription
	err = p1.PubSubSubscribe(topicName,
		func(msg *pubsub.Message) {
			p1Mu.Lock()
			p1Messages = append(p1Messages, string(msg.Data))
			p1Mu.Unlock()
		},
		func(err error) {
			p1Mu.Lock()
			p1Err = err
			p1Mu.Unlock()
		},
	)
	if err != nil {
		t.Fatalf("Failed to subscribe peer 1: %v", err)
	}

	// Peer 2 subscription
	err = p2.PubSubSubscribe(topicName,
		func(msg *pubsub.Message) {
			p2Mu.Lock()
			p2Messages = append(p2Messages, string(msg.Data))
			p2Mu.Unlock()
		},
		func(err error) {
			p2Mu.Lock()
			p2Err = err
			p2Mu.Unlock()
		},
	)
	if err != nil {
		t.Fatalf("Failed to subscribe peer 2: %v", err)
	}

	// Wait for subscriptions to be ready
	time.Sleep(2 * time.Second)

	// Publish messages from peer 1
	for i := 0; i < 5; i++ {
		msg := fmt.Sprintf("message-%d", i)
		err := p1.PubSubPublish(ctx, topicName, []byte(msg))
		if err != nil {
			t.Errorf("Failed to publish message %d: %v", i, err)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Wait for messages to propagate
	time.Sleep(2 * time.Second)

	// Verify both peers received messages
	p1Mu.Lock()
	_ = len(p1Messages) // p1Count
	p1Mu.Unlock()

	p2Mu.Lock()
	p2Count := len(p2Messages)
	p2Mu.Unlock()

	if p2Count < 5 {
		t.Errorf("Peer 2 did not receive all messages: got %d, expected 5", p2Count)
	}

	// Simulate connection shutdown by disconnecting peers
	p1.Peering().RemovePeer(p2.ID())
	p2.Peering().RemovePeer(p1.ID())

	// Wait for disconnection
	time.Sleep(2 * time.Second)

	// Check for subscription errors
	p1Mu.Lock()
	hasP1Err := p1Err != nil
	p1Mu.Unlock()

	p2Mu.Lock()
	hasP2Err := p2Err != nil
	p2Mu.Unlock()

	// Reconnect peers
	p1.Peering().AddPeer(peercore.AddrInfo{
		ID:    p2.ID(),
		Addrs: p2.Peer().Addrs(),
	})
	p2.Peering().AddPeer(peercore.AddrInfo{
		ID:    p1.ID(),
		Addrs: p1.Peer().Addrs(),
	})

	// Wait for reconnection
	time.Sleep(3 * time.Second)

	// Try to publish more messages
	for i := 5; i < 10; i++ {
		msg := fmt.Sprintf("message-%d", i)
		err := p1.PubSubPublish(ctx, topicName, []byte(msg))
		if err != nil {
			t.Errorf("Failed to publish message %d after reconnect: %v", i, err)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Wait for messages
	time.Sleep(2 * time.Second)

	// Check if messages were received after reconnection
	p2Mu.Lock()
	p2CountAfter := len(p2Messages)
	p2Mu.Unlock()

	if p2CountAfter < 10 {
		t.Errorf("After reconnection, peer 2 received %d messages, expected at least 10. This indicates desync.", p2CountAfter)
	}

	if hasP1Err || hasP2Err {
		t.Logf("Subscription errors occurred (this may be expected): p1=%v, p2=%v", hasP1Err, hasP2Err)
	}
}

// TestPubSubTopicReuseAfterError tests topic reuse after errors
func TestPubSubTopicReuseAfterError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dir1 := t.TempDir()

	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	port1 := 16000 + rnd.Intn(20000)

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
		t.Fatalf("Failed to create peer: %v", err)
	}
	defer p1.Close()

	time.Sleep(1 * time.Second)

	topicName := "test-reuse-topic"
	errorCount := 0
	var mu sync.Mutex

	// Subscribe with error handler
	err = p1.PubSubSubscribe(topicName,
		func(msg *pubsub.Message) {
			// Do nothing
		},
		func(err error) {
			mu.Lock()
			errorCount++
			mu.Unlock()
		},
	)
	if err != nil {
		t.Fatalf("Failed to subscribe: %v", err)
	}

	// Publish a message
	err = p1.PubSubPublish(ctx, topicName, []byte("test"))
	if err != nil {
		t.Errorf("Failed to publish: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	// Try to reuse the topic by subscribing again
	err = p1.PubSubSubscribe(topicName,
		func(msg *pubsub.Message) {
			// Do nothing
		},
		func(err error) {
			mu.Lock()
			errorCount++
			mu.Unlock()
		},
	)
	if err != nil {
		t.Errorf("Failed to reuse topic after first subscription: %v", err)
	}

	// Publish again
	err = p1.PubSubPublish(ctx, topicName, []byte("test2"))
	if err != nil {
		t.Errorf("Failed to publish after topic reuse: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	errCount := errorCount
	mu.Unlock()

	if errCount > 0 {
		t.Logf("Errors occurred during topic reuse test: %d", errCount)
	}
}

// TestPubSubMultipleSubscriptionsSameTopic tests multiple subscriptions to same topic
func TestPubSubMultipleSubscriptionsSameTopic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dir1 := t.TempDir()

	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	port1 := 17000 + rnd.Intn(20000)

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
		t.Fatalf("Failed to create peer: %v", err)
	}
	defer p1.Close()

	time.Sleep(1 * time.Second)

	topicName := "test-multi-sub-topic"
	messageCount := 0
	var mu sync.Mutex

	// Create multiple subscriptions to the same topic
	numSubs := 3
	for i := 0; i < numSubs; i++ {
		err = p1.PubSubSubscribe(topicName,
			func(msg *pubsub.Message) {
				mu.Lock()
				messageCount++
				mu.Unlock()
			},
			func(err error) {
				t.Logf("Subscription %d error: %v", i, err)
			},
		)
		if err != nil {
			t.Fatalf("Failed to create subscription %d: %v", i, err)
		}
	}

	time.Sleep(1 * time.Second)

	// Publish messages
	for i := 0; i < 5; i++ {
		err := p1.PubSubPublish(ctx, topicName, []byte(fmt.Sprintf("msg-%d", i)))
		if err != nil {
			t.Errorf("Failed to publish message %d: %v", i, err)
		}
		time.Sleep(100 * time.Millisecond)
	}

	time.Sleep(2 * time.Second)

	mu.Lock()
	count := messageCount
	mu.Unlock()

	// Each message should be received by each subscription
	expectedCount := 5 * numSubs
	if count < expectedCount {
		t.Errorf("Expected %d message deliveries, got %d", expectedCount, count)
	}
}
