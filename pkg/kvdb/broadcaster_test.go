package kvdb

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/taubyte/tau/p2p/peer"
)

func TestNewPubSubBroadcaster(t *testing.T) {
	ctx := context.Background()
	mockNode := peer.Mock(ctx)
	defer mockNode.Close()
	psub := mockNode.Messaging()

	broadcaster, err := NewPubSubBroadcaster(ctx, psub, "test-topic")
	if err != nil {
		t.Fatalf("Failed to create new broadcaster: %v", err)
	}
	if broadcaster == nil {
		t.Fatal("Expected non-nil broadcaster, got nil")
	}

	_, err = NewPubSubBroadcaster(ctx, psub, "test-topic")
	if err != nil {
		t.Fatalf("Failed to create broadcaster with existing topic: %v", err)
	}

	broadcaster.topic.Close()
}

func TestPubSubBroadcaster_Broadcast(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mockNode := peer.Mock(ctx)
	defer mockNode.Close()
	psub := mockNode.Messaging()

	broadcaster, _ := NewPubSubBroadcaster(ctx, psub, "test-topic")
	defer broadcaster.topic.Close()

	err := broadcaster.Broadcast(ctx, []byte("test message"))
	if err != nil {
		t.Fatalf("Failed to broadcast message: %v", err)
	}
}

func TestPubSubBroadcaster_Next(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mockNode := peer.Mock(ctx)
	defer mockNode.Close()
	psub := mockNode.Messaging()

	broadcaster, _ := NewPubSubBroadcaster(ctx, psub, "test-topic")
	defer broadcaster.topic.Close()

	go func() {
		time.Sleep(100 * time.Millisecond)
		broadcaster.Broadcast(ctx, []byte("test message"))
	}()

	msg, err := broadcaster.Next(ctx)
	if err != nil {
		t.Fatalf("Failed to receive message: %v", err)
	}
	if string(msg) != "test message" {
		t.Fatalf("Expected 'test message', got '%s'", msg)
	}
}

func TestPubSubBroadcaster_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	mockNode := peer.Mock(ctx)
	defer mockNode.Close()
	psub := mockNode.Messaging()

	broadcaster, _ := NewPubSubBroadcaster(ctx, psub, "test-topic-2")
	defer broadcaster.topic.Close()

	cancel()

	time.Sleep(100 * time.Millisecond)

	_, err := broadcaster.Next(ctx)
	if !errors.Is(err, ErrNoMoreBroadcast) {
		t.Fatalf("Expected ErrNoMoreBroadcast after context cancellation, got %v", err)
	}
}

func TestPubSubBroadcaster_ConcurrentOperations(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mockNode := peer.Mock(ctx)
	defer mockNode.Close()
	psub := mockNode.Messaging()

	broadcaster, _ := NewPubSubBroadcaster(ctx, psub, "test-topic")
	defer broadcaster.topic.Close()

	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		msg, err := broadcaster.Next(ctx)
		if err != nil {
			t.Errorf("Failed to receive message: %v", err)
		}
		if string(msg) != "test message" {
			t.Errorf("Expected 'test message', got '%s'", msg)
		}
	}()

	err := broadcaster.Broadcast(ctx, []byte("test message"))
	if err != nil {
		t.Fatalf("Failed to broadcast message: %v", err)
	}

	wg.Wait()
}

func TestPubSubBroadcaster_BroadcastAndReceive(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mockNode := peer.Mock(ctx)
	defer mockNode.Close()
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
		data, err := broadcaster.Next(ctx)
		if err != nil {
			t.Errorf("Failed to receive broadcast: %v", err)
			return
		}
		if string(data) != "testMessage" {
			t.Errorf("Expected message 'testMessage', got '%s'", data)
		}
	}()

	err = broadcaster.Broadcast(ctx, []byte("testMessage"))
	if err != nil {
		t.Fatalf("Failed to broadcast message: %v", err)
	}

	wg.Wait()
}

func TestPubSubBroadcaster_TopicRegistrationAndUnregistration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mockNode := peer.Mock(ctx)
	defer mockNode.Close()
	psub := mockNode.Messaging()

	topic := "testTopicRegistration"
	key := broadcasterKey{psub: psub, topic: topic}
	_, err := NewPubSubBroadcaster(ctx, psub, topic)
	if err != nil {
		t.Fatalf("Failed to create new PubSubBroadcaster: %v", err)
	}

	if getTopic(key) == nil {
		t.Errorf("Expected topic '%s' to be registered", topic)
	}

	cancel()

	time.Sleep(100 * time.Millisecond)

	if getTopic(key) != nil {
		t.Errorf("Expected topic '%s' to be unregistered after context cancellation", topic)
	}
}
