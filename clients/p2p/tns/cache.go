package tns

import (
	"context"
	"fmt"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/taubyte/go-interfaces/services/tns"
	"github.com/taubyte/p2p/peer"
	"github.com/taubyte/tau/clients/p2p/tns/common"
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
	c.lock.Lock()
	defer c.lock.Unlock()

	sub, err := c.listen(key)
	if err != nil {
		logger.Error(err)
		return
	}

	c.data[key.String()] = value

	go func() {
		ctx, ctxC := context.WithTimeout(sub.virtualCtx, common.ClientKeyCacheLifetime)
		defer ctxC()

		<-ctx.Done()
		c.lock.Lock()
		delete(c.data, key.String())
		c.lock.Unlock()
	}()
}

func (c *cache) listen(key tns.Path) (*subscription, error) {
	topic := common.GetChannelFor(key.Slice()...)

	// Locked by the caller
	sub, ok := c.subscriptions[topic]
	if ok {
		// TODO: Update timeout
		return sub, nil
	}

	sub = &subscription{}
	sub.ctx, sub.ctxC = context.WithCancel(c.node.Context())
	sub.virtualCtx, sub.virtualCtxC = context.WithCancel(sub.ctx)

	err := c.node.PubSubSubscribeContext(sub.ctx, topic, func(msg *pubsub.Message) {
		sub.virtualCtxC()
		sub.virtualCtx, sub.virtualCtxC = context.WithCancel(sub.ctx)
	}, func(err error) {
		sub.ctxC()
	})
	if err != nil {
		return sub, fmt.Errorf("subscription to key `%s`||`%s` failed to initialize with: %s", key.String(), topic, err.Error())
	}

	c.subscriptions[topic] = sub

	go func() {
		<-sub.ctx.Done()
		c.lock.Lock()
		delete(c.subscriptions, topic)
		c.lock.Unlock()
	}()

	return sub, nil
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
