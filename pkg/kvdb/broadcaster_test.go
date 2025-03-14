package kvdb

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	crdt "github.com/ipfs/go-ds-crdt"
	"github.com/taubyte/tau/p2p/peer"
)

// TestNewPubSubBroadcaster tests the creation of a new PubSubBroadcaster.
func TestNewPubSubBroadcaster(t *testing.T) {
	ctx := context.Background()
	mockNode := peer.Mock(ctx)
	psub := mockNode.Messaging()

	// Test successful creation
	broadcaster, err := NewPubSubBroadcaster(ctx, psub, "test-topic")
	if err != nil {
		t.Fatalf("Failed to create new broadcaster: %v", err)
	}
	if broadcaster == nil {
		t.Fatal("Expected non-nil broadcaster, got nil")
	}

	// Test creation with existing topic
	_, err = NewPubSubBroadcaster(ctx, psub, "test-topic")
	if err != nil {
		t.Fatalf("Failed to create broadcaster with existing topic: %v", err)
	}

	// Cleanup
	broadcaster.topic.Close()
}

// TestPubSubBroadcaster_Broadcast tests the broadcast functionality.
func TestPubSubBroadcaster_Broadcast(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mockNode := peer.Mock(ctx)
	psub := mockNode.Messaging()

	broadcaster, _ := NewPubSubBroadcaster(ctx, psub, "test-topic")
	defer broadcaster.topic.Close()

	// Test broadcasting a message
	err := broadcaster.Broadcast([]byte("test message"))
	if err != nil {
		t.Fatalf("Failed to broadcast message: %v", err)
	}
}

// TestPubSubBroadcaster_Next tests the receiving of messages.
func TestPubSubBroadcaster_Next(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mockNode := peer.Mock(ctx)
	psub := mockNode.Messaging()

	broadcaster, _ := NewPubSubBroadcaster(ctx, psub, "test-topic")
	defer broadcaster.topic.Close()

	// Start a goroutine to broadcast a message after a delay
	go func() {
		time.Sleep(100 * time.Millisecond)
		broadcaster.Broadcast([]byte("test message"))
	}()

	// Test receiving a message
	msg, err := broadcaster.Next()
	if err != nil {
		t.Fatalf("Failed to receive message: %v", err)
	}
	if string(msg) != "test message" {
		t.Fatalf("Expected 'test message', got '%s'", msg)
	}
}

// TestPubSubBroadcaster_ContextCancellation tests the behavior when the context is cancelled.
func TestPubSubBroadcaster_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	mockNode := peer.Mock(ctx)
	psub := mockNode.Messaging()

	broadcaster, _ := NewPubSubBroadcaster(ctx, psub, "test-topic-2")
	defer broadcaster.topic.Close()

	// Cancel the context
	cancel()

	time.Sleep(100 * time.Millisecond)

	// Test that Next returns an error after context cancellation
	_, err := broadcaster.Next()
	if !errors.Is(err, crdt.ErrNoMoreBroadcast) {
		t.Fatalf("Expected ErrNoMoreBroadcast after context cancellation, got %v", err)
	}
}

// TestPubSubBroadcaster_ConcurrentOperations tests concurrent broadcasting and receiving.
func TestPubSubBroadcaster_ConcurrentOperations(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mockNode := peer.Mock(ctx)
	psub := mockNode.Messaging()

	broadcaster, _ := NewPubSubBroadcaster(ctx, psub, "test-topic")
	defer broadcaster.topic.Close()

	var wg sync.WaitGroup
	wg.Add(1)

	// Start a goroutine to receive a message
	go func() {
		defer wg.Done()
		msg, err := broadcaster.Next()
		if err != nil {
			t.Errorf("Failed to receive message: %v", err)
		}
		if string(msg) != "test message" {
			t.Errorf("Expected 'test message', got '%s'", msg)
		}
	}()

	// Broadcast a message
	err := broadcaster.Broadcast([]byte("test message"))
	if err != nil {
		t.Fatalf("Failed to broadcast message: %v", err)
	}

	wg.Wait()
}

func TestPubSubBroadcaster_BroadcastAndReceive(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mockNode := peer.Mock(ctx)
	psub := mockNode.Messaging()

	topic := "testTopic"
	broadcaster, err := NewPubSubBroadcaster(ctx, psub, topic)
	if err != nil {
		t.Fatalf("Failed to create new PubSubBroadcaster: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		data, err := broadcaster.Next()
		if err != nil {
			t.Errorf("Failed to receive broadcast: %v", err)
			return
		}
		if string(data) != "testMessage" {
			t.Errorf("Expected message 'testMessage', got '%s'", data)
		}
	}()

	err = broadcaster.Broadcast([]byte("testMessage"))
	if err != nil {
		t.Fatalf("Failed to broadcast message: %v", err)
	}

	wg.Wait()
}

func TestPubSubBroadcaster_TopicRegistrationAndUnregistration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mockNode := peer.Mock(ctx)
	psub := mockNode.Messaging()

	topic := "testTopicRegistration"
	_, err := NewPubSubBroadcaster(ctx, psub, topic)
	if err != nil {
		t.Fatalf("Failed to create new PubSubBroadcaster: %v", err)
	}

	if getTopic(topic) == nil {
		t.Errorf("Expected topic '%s' to be registered", topic)
	}

	cancel() // This should unregister the topic

	time.Sleep(100 * time.Millisecond) // Give some time for the goroutine to unregister the topic

	if getTopic(topic) != nil {
		t.Errorf("Expected topic '%s' to be unregistered after context cancellation", topic)
	}
}
