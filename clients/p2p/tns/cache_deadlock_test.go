package tns

import (
	"context"
	"testing"
	"time"

	"github.com/taubyte/tau/p2p/peer"
	spec "github.com/taubyte/tau/pkg/specs/common"
)

// fullQueueNode fills a new subscription's queue before Subscribe returns, as
// invalidations arriving before the reader starts would.
type fullQueueNode struct{ peer.Node }

func (n fullQueueNode) PubSubSubscribeContext(ctx context.Context, name string, handler peer.PubSubConsumerHandler, errHandler peer.PubSubConsumerErrorHandler) error {
	if err := n.Node.PubSubSubscribeContext(ctx, name, handler, errHandler); err != nil {
		return err
	}
	for range 8 {
		handler(nil)
	}
	return nil
}

// The queue must have a reader before anything can fill it: the creator used
// to send first and start the reader after, so a full queue hung it and every
// later request for that key, with no timeout.
func TestListenDoesNotHangOnAFullQueue(t *testing.T) {
	node := peer.Mock(t.Context())
	defer node.Close()
	c := newCache(fullQueueNode{node})

	done := make(chan error, 1)
	go func() {
		_, _, err := c.listen(spec.NewTnsPath([]string{"projects", "p3"}))
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("listen hung on a full queue")
	}
}
