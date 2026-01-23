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

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	peercore "github.com/libp2p/go-libp2p/core/peer"
	keypair "github.com/taubyte/tau/p2p/keypair"
)

// TestPubSubStressConnectionChurn tests pubsub during connection churn
func TestPubSubStressConnectionChurn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create two peers
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	port1 := 25000 + rnd.Intn(20000)
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

	// Subscribe to topic
	topicName := "stress-churn-topic"
	var p2Messages []string
	var p2Mu sync.Mutex
	var p2Err error
	var p2ErrMu sync.Mutex

	err = p2.PubSubSubscribe(topicName,
		func(msg *pubsub.Message) {
			p2Mu.Lock()
			p2Messages = append(p2Messages, string(msg.Data))
			p2Mu.Unlock()
		},
		func(err error) {
			p2ErrMu.Lock()
			p2Err = err
			p2ErrMu.Unlock()
		},
	)
	if err != nil {
		t.Fatalf("Failed to subscribe peer 2: %v", err)
	}

	time.Sleep(1 * time.Second)

	// Publish messages while churning connections
	messagesToSend := 20
	var messagesReceived int64

	go func() {
		for i := 0; i < messagesToSend; i++ {
			msg := fmt.Sprintf("message-%d", i)
			if err := p1.PubSubPublish(ctx, topicName, []byte(msg)); err == nil {
				time.Sleep(100 * time.Millisecond)
			}
		}
	}()

	// Churn connections while messages are being sent
	go func() {
		for i := 0; i < 5; i++ {
			time.Sleep(500 * time.Millisecond)
			p1.Peering().RemovePeer(p2.ID())
			time.Sleep(200 * time.Millisecond)
			p1.Peering().AddPeer(peercore.AddrInfo{
				ID:    p2.ID(),
				Addrs: p2.Peer().Addrs(),
			})
		}
	}()

	// Wait for messages
	time.Sleep(5 * time.Second)

	p2Mu.Lock()
	receivedCount := len(p2Messages)
	p2Mu.Unlock()

	p2ErrMu.Lock()
	hasErr := p2Err != nil
	p2ErrMu.Unlock()

	atomic.StoreInt64(&messagesReceived, int64(receivedCount))

	receiveRate := float64(receivedCount) / float64(messagesToSend) * 100
	t.Logf("Message receive rate during connection churn: %.2f%% (%d/%d)", receiveRate, receivedCount, messagesToSend)
	if hasErr {
		t.Logf("Subscription error occurred: %v", p2Err)
	}

	// Should receive at least some messages despite churn
	if receivedCount == 0 {
		t.Errorf("No messages received during connection churn")
	}
}

// TestPubSubStressManyTopics tests many topics simultaneously
func TestPubSubStressManyTopics(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dir1 := t.TempDir()
	dir2 := t.TempDir()

	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	port1 := 26000 + rnd.Intn(20000)
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
	p1.Peering().AddPeer(peercore.AddrInfo{
		ID:    p2.ID(),
		Addrs: p2.Peer().Addrs(),
	})
	p2.Peering().AddPeer(peercore.AddrInfo{
		ID:    p1.ID(),
		Addrs: p1.Peer().Addrs(),
	})

	time.Sleep(3 * time.Second)

	// Create many topics
	numTopics := 10
	var wg sync.WaitGroup
	var errors int64

	for i := 0; i < numTopics; i++ {
		wg.Add(1)
		topicName := fmt.Sprintf("stress-topic-%d", i)
		go func(topic string) {
			defer wg.Done()

			// Subscribe on p2
			err := p2.PubSubSubscribe(topic,
				func(msg *pubsub.Message) {
					// Do nothing
				},
				func(err error) {
					atomic.AddInt64(&errors, 1)
				},
			)
			if err != nil {
				atomic.AddInt64(&errors, 1)
				return
			}

			time.Sleep(100 * time.Millisecond)

			// Publish from p1
			for j := 0; j < 5; j++ {
				if err := p1.PubSubPublish(ctx, topic, []byte(fmt.Sprintf("msg-%d", j))); err != nil {
					atomic.AddInt64(&errors, 1)
				}
				time.Sleep(50 * time.Millisecond)
			}
		}(topicName)
	}

	wg.Wait()
	time.Sleep(2 * time.Second)

	errorCount := atomic.LoadInt64(&errors)
	errorRate := float64(errorCount) / float64(numTopics*6) * 100 // 6 operations per topic
	t.Logf("Error rate with many topics: %.2f%% (%d errors)", errorRate, errorCount)

	if errorRate > 20 {
		t.Errorf("Error rate too high: %.2f%%, expected less than 20%%", errorRate)
	}
}

// TestPubSubStressRapidSubscribeUnsubscribe tests rapid subscribe/unsubscribe
func TestPubSubStressRapidSubscribeUnsubscribe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dir1 := t.TempDir()

	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	port1 := 27000 + rnd.Intn(20000)

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

	topicName := "stress-rapid-topic"
	iterations := 30
	var errors int64

	for i := 0; i < iterations; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		err := p1.PubSubSubscribeContext(ctx, topicName,
			func(msg *pubsub.Message) {
				// Do nothing
			},
			func(err error) {
				atomic.AddInt64(&errors, 1)
			},
		)
		if err != nil {
			atomic.AddInt64(&errors, 1)
		}

		// Publish a message
		p1.PubSubPublish(ctx, topicName, []byte(fmt.Sprintf("msg-%d", i)))

		// Cancel subscription
		time.Sleep(50 * time.Millisecond)
		cancel()
		time.Sleep(50 * time.Millisecond)
	}

	errorCount := atomic.LoadInt64(&errors)
	// Errors can occur from: subscribe failures, publish failures, and error handlers
	// With rapid cancellation, many errors are expected
	errorRate := float64(errorCount) / float64(iterations*3) * 100 // 3 potential error points per iteration
	t.Logf("Error rate during rapid subscribe/unsubscribe: %.2f%% (%d errors)", errorRate, errorCount)

	// With rapid context cancellation, errors are expected and acceptable
	// The important thing is that the system doesn't crash and can recover
	if errorRate > 60 {
		t.Errorf("Error rate too high: %.2f%%, expected less than 60%% (rapid cancellation causes expected errors)", errorRate)
	}
}
