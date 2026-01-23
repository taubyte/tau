package peer

import (
	"context"
	"time"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	peer "github.com/libp2p/go-libp2p/core/peer"
)

type PubSubConsumerHandler func(msg *pubsub.Message)
type PubSubConsumerErrorHandler func(err error)

func (p *node) NewPubSubKeepAlive(ctx context.Context, cancel context.CancelFunc, name string) error {
	// Use a special pubsub topic to avoid disconnecting
	// from globaldb peers.

	if p.closed.Load() {
		return errorClosed
	}

	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				p.PubSubPublish(ctx, name, []byte(name))
			}
		}
	}()

	peers := make(map[peer.ID]struct{})

	return p.PubSubSubscribe(
		name,
		func(msg *pubsub.Message) {
			if _, exists := peers[msg.ReceivedFrom]; !exists {
				peers[msg.ReceivedFrom] = struct{}{}
				p.host.ConnManager().Protect(msg.ReceivedFrom, "/keep/"+name)
			}
		},
		func(err error) {
			for pid := range peers {
				p.host.ConnManager().Unprotect(pid, "/keep/"+name)
			}
			peers = nil
			cancel()
		},
	)
}

func (p *node) getOrCreateTopic(name string) (topic *pubsub.Topic, err error) {
	if p.closed.Load() {
		return nil, errorClosed
	}

	p.topicsMutex.Lock()
	defer p.topicsMutex.Unlock()

	var ok bool
	topic, ok = p.topics[name]
	if !ok {
		if topic, err = p.messaging.Join(name); err != nil {
			return
		}

		p.topics[name] = topic
	}

	return topic, nil
}

func (p *node) PubSubSubscribe(name string, handler PubSubConsumerHandler, err_handler PubSubConsumerErrorHandler) error {
	if p.closed.Load() {
		return errorClosed
	}

	topic, err := p.getOrCreateTopic(name)
	if err != nil {
		return err
	}

	return p.PubSubSubscribeToTopic(topic, handler, err_handler)
}

func (p *node) PubSubSubscribeToTopic(topic *pubsub.Topic, handler PubSubConsumerHandler, err_handler PubSubConsumerErrorHandler) error {
	if p.closed.Load() {
		return errorClosed
	}

	subs, err := topic.Subscribe()
	if err != nil {
		return err
	}

	go func() {
		defer subs.Cancel()
		for {
			select {
			case <-p.ctx.Done():
				return
			default:
				msg, err := subs.Next(p.ctx)
				if err != nil {
					err_handler(err)
					return
				}
				// Note: libp2p's GossipSub already handles message deduplication internally
				// via its seen message cache, so we don't need custom deduplication here.
				handler(msg)
			}
		}
	}()

	return nil
}

// TODO: make PubSubSubscribe not recreate topics,  should cache and open.
func (p *node) PubSubSubscribeContext(ctx context.Context, name string, handler PubSubConsumerHandler, err_handler PubSubConsumerErrorHandler) error {
	if p.closed.Load() {
		return errorClosed
	}

	topic, err := p.getOrCreateTopic(name)
	if err != nil {
		return err
	}

	subs, err := topic.Subscribe()
	if err != nil {
		return err
	}

	go func() {
		defer subs.Cancel()
		for {
			select {
			case <-ctx.Done():
				return
			default:
				msg, err := subs.Next(ctx)
				if err != nil {
					err_handler(err)
					return
				}
				handler(msg)
			}
		}
	}()

	return nil
}

func (p *node) PubSubPublish(ctx context.Context, name string, data []byte) error {
	if p.closed.Load() {
		return errorClosed
	}

	topic, err := p.getOrCreateTopic(name)
	if err != nil {
		return err
	}

	return topic.Publish(ctx, data)
}
