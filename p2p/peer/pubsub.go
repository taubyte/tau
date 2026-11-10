package peer

import (
	"context"
	"fmt"
	"time"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/discovery"
	"github.com/libp2p/go-libp2p/core/host"
	peer "github.com/libp2p/go-libp2p/core/peer"
)

// dialableDiscovery drops providers with no known address. Pubsub would dial
// each one through the routed host, which falls back to a full DHT FindPeer
// for every topic round: such providers (client nodes: loopback-only, new key
// each boot) cannot be reached anyway.
type dialableDiscovery struct {
	discovery.Discovery
	host host.Host
}

func (d dialableDiscovery) FindPeers(ctx context.Context, ns string, opts ...discovery.Option) (<-chan peer.AddrInfo, error) {
	in, err := d.Discovery.FindPeers(ctx, ns, opts...)
	if err != nil {
		return nil, err
	}
	out := make(chan peer.AddrInfo)
	go func() {
		defer close(out)
		for pi := range in {
			if len(pi.Addrs) == 0 && len(d.host.Peerstore().Addrs(pi.ID)) == 0 {
				continue
			}
			select {
			case out <- pi:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

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
			return nil, fmt.Errorf("joining pubsub topic %q failed: %w", name, err)
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
		return fmt.Errorf("getting topic %q for subscription failed: %w", name, err)
	}

	return p.PubSubSubscribeToTopic(topic, handler, err_handler)
}

func (p *node) PubSubSubscribeToTopic(topic *pubsub.Topic, handler PubSubConsumerHandler, err_handler PubSubConsumerErrorHandler) error {
	if p.closed.Load() {
		return errorClosed
	}

	subs, err := topic.Subscribe()
	if err != nil {
		return fmt.Errorf("subscribing to pubsub topic failed: %w", err)
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
					if p.ctx.Err() == nil {
						err_handler(err)
					}
					return
				}
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
		return fmt.Errorf("getting topic %q for context subscription failed: %w", name, err)
	}

	subs, err := topic.Subscribe()
	if err != nil {
		return fmt.Errorf("subscribing to pubsub topic %q failed: %w", name, err)
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
					if ctx.Err() == nil {
						err_handler(err)
					}
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
		return fmt.Errorf("getting topic %q for publish failed: %w", name, err)
	}

	if err := topic.Publish(ctx, data); err != nil {
		return fmt.Errorf("publishing to topic %q failed: %w", name, err)
	}

	return nil
}
