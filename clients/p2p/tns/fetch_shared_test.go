package tns

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/taubyte/tau/p2p/peer"
	spec "github.com/taubyte/tau/pkg/specs/common"
)

// Every key's cache entry expires at once, so under load hundreds of requests
// miss together. One stream per miss hit the TNS node's stream limit.
func TestConcurrentMissesShareOneFetch(t *testing.T) {
	node := peer.Mock(t.Context())
	defer node.Close()
	c := &Client{cache: newCache(node)}

	var calls atomic.Int32
	release := make(chan struct{})
	fetch := func([]string) (interface{}, error) {
		calls.Add(1)
		<-release
		return "value", nil
	}

	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			path := spec.NewTnsPath([]string{"projects", "p4"}) // per request, as in production: String caches
			if v, err := c.fetchShared(path, fetch); err != nil || v != "value" {
				t.Errorf("got %v, %v", v, err)
			}
		}()
	}
	time.Sleep(200 * time.Millisecond)
	close(release)
	wg.Wait()

	if n := calls.Load(); n != 1 {
		t.Fatalf("%d fetches for one key, want 1", n)
	}
}
