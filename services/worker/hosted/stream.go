package hosted

import (
	"context"
	"time"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/specs"
	"github.com/taubyte/tau/services/worker"
	"github.com/taubyte/tau/services/worker/stream"
)

// The stream backings are services/worker/stream's: the worker decides which invoker
// and vault a publish and a subscription go through. The bus is its node's.

// streamPublisher backs `taubyte:stream/publisher`, and keeps the consumers that
// deliver a stream on a log to this node's subscribers.
func (n *Service) streamPublisher() (*stream.Publisher, error) {
	publisher, err := stream.NewPublisher(workerSubscribers{node: n}, placed{node: n}, vaultSecrets{node: n})
	if err != nil {
		return nil, err
	}
	n.addCloser(publisher)

	n.subscriptions, err = stream.NewSubscriptions(placed{node: n}, vaultSecrets{node: n})
	if err != nil {
		return nil, err
	}
	return publisher, nil
}

// subscribeEvery is how often a node matches its consumers to the subscribers in the
// deployments it holds.
const subscribeEvery = 2 * time.Second

// subscribe keeps the consumers matched until the node closes, then stops them.
func (n *Service) subscribe(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	defer n.subscriptions.Close()

	tick := time.NewTicker(subscribeEvery)
	defer tick.Stop()
	for {
		n.subscriptions.Reconcile(n.worker.Subscribers(ctx))
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// workerSubscribers reads the worker when asked, since the publisher is built before it.
type workerSubscribers struct{ node *Service }

func (w workerSubscribers) SubscribersOf(ctx context.Context, handle string) ([]worker.Subscriber, error) {
	if w.node.worker == nil {
		return nil, nil
	}
	return w.node.worker.SubscribersOf(ctx, handle)
}

// placed runs a workload through placement, or on this node when it has none. A
// delivery is not run where it was taken: a claim is leader-only and a publish is
// wherever its publisher ran, so either would pile work onto one node.
type placed struct{ node *Service }

func (p placed) Invoke(ctx context.Context, at specs.Address, req *invoke.Request) (*invoke.Response, error) {
	if p.node.workers == nil {
		return p.node.worker.Invoke(ctx, at, req)
	}
	return p.node.workers.Invoke(ctx, at, req)
}
