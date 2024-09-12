package tns

import (
	"context"
	"fmt"
	"time"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/taubyte/tau/clients/p2p/tns/common"
	"github.com/taubyte/tau/core/services/tns"
	"github.com/taubyte/tau/p2p/peer"
)

func newCache(node peer.Node) *cache {
	return &cache{
		node:          node,
		subscriptions: map[string]*subscription{},
		data:          map[string]interface{}{},
	}
}

func (c *cache) close() {
	c.lock.Lock()
	c.subscriptions = nil
	c.data = nil
	c.lock.Unlock()
}

func (c *cache) put(key tns.Path, value interface{}) {

	_, err := c.listen(key)
	if err != nil {
		logger.Error(err)
		return
	}

	c.lock.Lock()
	defer c.lock.Unlock()
	c.data[key.String()] = value
}

func (c *cache) listen(key tns.Path) (*subscription, error) {
	topic := common.GetChannelFor(key.Slice()...)

	c.lock.RLock()
	sub, ok := c.subscriptions[topic]
	c.lock.RUnlock()
	if ok {
		sub.key <- key.String()
		return sub, nil
	}

	sub = &subscription{
		cache:    c,
		topic:    topic,
		key:      make(chan string, 8),
		keys:     make([]string, 0),
		deadline: time.Now().Add(common.ClientKeyCacheLifetime),
	}
	sub.ctx, sub.ctxC = context.WithCancel(c.node.Context())

	err := c.node.PubSubSubscribeContext(sub.ctx, topic, func(msg *pubsub.Message) {
		// TODO: implement tns Publish and optimize this
		sub.deadline = sub.deadline.Add(common.ClientKeyCacheLifetime)
		sub.key <- "" // TODO: do better later but for now "" mean drop all the keys
	}, func(err error) {
		sub.ctxC()
	})
	if err != nil {
		return sub, fmt.Errorf("subscription to key `%s`||`%s` failed to initialize with: %s", key.String(), topic, err.Error())
	}

	c.lock.Lock()
	c.subscriptions[topic] = sub
	c.lock.Unlock()

	sub.key <- key.String()

	sub.watch()

	return sub, nil
}

func (sub *subscription) watch() {
	go func() {
		for {
			select {
			case <-sub.ctx.Done():
				sub.close()
				return
			case k := <-sub.key:
				if k == "" {
					for _, k := range sub.keys {
						sub.cache.lock.Lock()
						delete(sub.cache.data, k)
						sub.cache.lock.Unlock()
					}
					sub.keys = make([]string, 0) // TODO: maybe use reflect to just adjust the size so we don't have to reallocate
				} else {
					sub.keys = append(sub.keys, k)
				}
			case cur := <-time.After(time.Until(sub.deadline)):
				if cur.After(sub.deadline) {
					sub.close()
					return
				}
			}
		}
	}()
}

func (sub *subscription) close() {
	sub.ctxC()
	sub.cache.lock.Lock()
	delete(sub.cache.subscriptions, sub.topic)
	for _, k := range sub.keys {
		delete(sub.cache.data, k)
	}
	close(sub.key)
	sub.keys = nil
	sub.cache.lock.Unlock()
}

func (c *cache) get(key tns.Path) (value interface{}) {
	c.lock.RLock()
	value, ok := c.data[key.String()]
	c.lock.RUnlock()

	if !ok {
		return nil
	}

	return value
}
