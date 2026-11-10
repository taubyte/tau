package tns

import (
	"testing"
	"time"

	"github.com/taubyte/tau/clients/p2p/tns/common"
	"github.com/taubyte/tau/p2p/peer"
	spec "github.com/taubyte/tau/pkg/specs/common"
)

// A value fetched while a push for its key was published must not be cached:
// the next read would serve the old value until the subscription expired.
func TestCacheDropsAFetchThatRacedAPush(t *testing.T) {
	node := peer.Mock(t.Context())
	defer node.Close()
	c := newCache(node)
	key := spec.NewTnsPath([]string{"projects", "p1", "functions"})

	sub, gen, err := c.listen(key)
	if err != nil {
		t.Fatal(err)
	}

	// The push lands while the fetch is in flight.
	if err := node.PubSubPublish(t.Context(), common.GetChannelFor(key.Slice()...), nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for sub.gen.Load() == gen {
		if time.Now().After(deadline) {
			t.Fatal("invalidation never arrived")
		}
		time.Sleep(10 * time.Millisecond)
	}

	c.store(sub, gen, key, "stale")
	if v := c.get(key); v != nil {
		t.Fatalf("a fetch that raced a push was cached: %v", v)
	}
}

// Sending to a subscription that has ended is dropped, not a panic: it used to
// close its channel while the pubsub handler could still send to it.
func TestClosedSubscriptionIgnoresLateKeys(t *testing.T) {
	node := peer.Mock(t.Context())
	defer node.Close()
	c := newCache(node)
	sub, _, err := c.listen(spec.NewTnsPath([]string{"projects", "p2"}))
	if err != nil {
		t.Fatal(err)
	}
	sub.ctxC() // what an expiry or a subscription error does
	sub.send("late")
	sub.send("")
}
