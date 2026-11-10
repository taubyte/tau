package tns

import (
	"context"
	"fmt"
	"math/rand/v2"
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

// store caches value unless the key's topic was invalidated since gen was
// read: a fetch that raced a push must not be kept as current.
func (c *cache) store(sub *subscription, gen uint64, key tns.Path, value interface{}) {
	if sub.gen.Load() != gen || sub.ctx.Err() != nil {
		return
	}
	c.lock.Lock()
	defer c.lock.Unlock()
	if c.data != nil {
		c.data[key.String()] = value
	}
}

// initialLifetime spreads the first expiry so keys cached together do not all miss at once.
func initialLifetime() time.Duration {
	return common.ClientKeyCacheLifetime + rand.N(common.ClientKeyCacheLifetime/5+1)
}

// listen subscribes to the key's invalidations before it is fetched, and
// returns the subscription's generation at that moment for store.
func (c *cache) listen(key tns.Path) (*subscription, uint64, error) {
	topic := common.GetChannelFor(key.Slice()...)

	var (
		sub *subscription
		ok  bool
	)

	c.lock.RLock()
	if c.subscriptions != nil {
		sub, ok = c.subscriptions[topic]
	}
	c.lock.RUnlock()

	if ok {
		gen := sub.gen.Load()
		sub.send(key.String())
		return sub, gen, nil
	}

	sub = &subscription{
		cache:    c,
		topic:    topic,
		key:      make(chan string, 8),
		keys:     make([]string, 0),
		deadline: time.Now().Add(initialLifetime()),
	}
	sub.ctx, sub.ctxC = context.WithCancel(c.node.Context())

	err := c.node.PubSubSubscribeContext(sub.ctx, topic, func(msg *pubsub.Message) {
		// TODO: implement tns Publish and optimize this
		sub.gen.Add(1)
		sub.send("") // "" means drop all the keys
	}, func(err error) {
		sub.ctxC()
	})
	if err != nil {
		return sub, 0, fmt.Errorf("subscription to key `%s`||`%s` failed to initialize with: %s", key.String(), topic, err.Error())
	}

	// Before anything can fill the queue: a full queue with no reader hangs
	// this request and every later one for the key.
	sub.watch()

	c.lock.Lock()
	if c.subscriptions != nil {
		c.subscriptions[topic] = sub
	}
	c.lock.Unlock()

	gen := sub.gen.Load()
	sub.send(key.String())

	return sub, gen, nil
}

// send hands a key to the watch loop, or drops it once the subscription is
// closed: the channel is never closed, so a late sender cannot panic.
func (sub *subscription) send(k string) {
	select {
	case sub.key <- k:
	case <-sub.ctx.Done():
	}
}

// watch owns keys and deadline after creation, so neither needs a lock.
func (sub *subscription) watch() {
	go func() {
		for {
			select {
			case <-sub.ctx.Done():
				sub.close()
				return
			case k := <-sub.key:
				if k == "" {
					sub.deadline = sub.deadline.Add(common.ClientKeyCacheLifetime)
					for _, k := range sub.keys {
						sub.cache.lock.Lock()
						if sub.cache.data != nil {
							delete(sub.cache.data, k)
						}
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
	if sub.cache.subscriptions != nil && sub.cache.subscriptions[sub.topic] == sub {
		delete(sub.cache.subscriptions, sub.topic)
	}
	if sub.cache.data != nil {
		for _, k := range sub.keys {
			delete(sub.cache.data, k)
		}
	}
	sub.keys = nil
	sub.cache.lock.Unlock()
}

func (c *cache) get(key tns.Path) (value interface{}) {
	c.lock.RLock()
	if c.data != nil {
		value = c.data[key.String()]
	}
	c.lock.RUnlock()

	return value
}
