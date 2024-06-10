package peer

import (
	"context"
	"time"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
)

type PubSubConsumerHandler func(msg *pubsub.Message)
type PubSubConsumerErrorHandler func(err error)

func (p *node) NewPubSubKeepAlive(ctx context.Context, cancel context.CancelFunc, name string) error {
	// Use a special pubsub topic to avoid disconnecting
	// from globaldb peers.

	if !p.closed {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-time.After(20 * time.Second):
					p.PubSubPublish(ctx, name, []byte(name))
				}
			}
		}()

		return p.PubSubSubscribe(
			name,
			func(msg *pubsub.Message) {
				p.host.ConnManager().TagPeer(msg.ReceivedFrom, "keep", 100)
			},
			func(err error) {
				cancel()
			},
		)
	}

	return errorClosed
}

func (p *node) getOrCreateTopic(name string) (topic *pubsub.Topic, err error) {
	if !p.closed {
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

	err = errorClosed
	return
}

func (p *node) PubSubSubscribe(name string, handler PubSubConsumerHandler, err_handler PubSubConsumerErrorHandler) error {
	if !p.closed {
		topic, err := p.getOrCreateTopic(name)
		if err != nil {
			return err
		}

		return p.PubSubSubscribeToTopic(topic, handler, err_handler)
	}

	return errorClosed
}

func (p *node) PubSubSubscribeToTopic(topic *pubsub.Topic, handler PubSubConsumerHandler, err_handler PubSubConsumerErrorHandler) error {
	if !p.closed {
		subs, err := topic.Subscribe()
		if err != nil {
			return err
		}

		go func() {
			lookup := make(map[string]struct{})
			max := 1024
			order := make([]string, 0, max)

			defer subs.Cancel()
			for {
				select {
				case <-p.ctx.Done():
					return
				default:
					msg, err := subs.Next(p.ctx)
					if err != nil {
						err_handler(err)
						break
					}

					if _, ok := lookup[msg.ID]; ok {
						continue
					}

					lookup[msg.ID] = struct{}{}
					if len(order)+1 >= cap(order) {
						delete(lookup, order[0])
						order = order[1:]
					}
					order = append(order, msg.ID)

					handler(msg)
				}
			}
		}()

		return nil
	}

	return errorClosed
}

// TODO: make PubSubSubscribe not recreate topics,  should cache and open.
func (p *node) PubSubSubscribeContext(ctx context.Context, name string, handler PubSubConsumerHandler, err_handler PubSubConsumerErrorHandler) error {
	if !p.closed {
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
						break
					}
					handler(msg)
				}
			}
		}()

		return nil
	}

	return errorClosed
}

func (p *node) PubSubPublish(ctx context.Context, name string, data []byte) error {
	if !p.closed {
		topic, err := p.getOrCreateTopic(name)
		if err != nil {
			return err
		}

		return topic.Publish(ctx, data)
	}

	return errorClosed
}
