package instance

import (
	"context"
	"time"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/specs"
	"github.com/taubyte/tau/services/worker"
	"github.com/taubyte/tau/services/worker/stream"
)

// The stream and bus backings are services/worker/stream's; the root decides which
// gossip, cloud, invoker and vault, and that a bus has one backing shared by the wasm
// tier and the gateway.

// busStreams is this node's bus backing, built on first use.
//
// It is a method rather than a line in openWorker because both the wasm tier and
// the gateway want it and a node may run either without the other.
func (n *Instance) busStreams() *stream.Bus {
	if n.bus == nil {
		n.bus = stream.NewBus(n.Messaging(), n.cfg.Cloud)
		n.addCloser(n.bus)
	}
	return n.bus
}

// BusTopic is what a bus's handle goes on the wire as. Whatever comes to attach
// a browser has to listen on exactly what a publish reaches.
func (n *Instance) BusTopic(handle string) string {
	return stream.Topic(n.cfg.Cloud, handle)
}

// streamPublisher backs `taubyte:stream/publisher`, and keeps the consumers that
// deliver a stream on a log to this node's subscribers.
func (n *Instance) streamPublisher() (*stream.Publisher, error) {
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
func (n *Instance) subscribe(ctx context.Context, done chan<- struct{}) {
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
type workerSubscribers struct{ node *Instance }

func (w workerSubscribers) SubscribersOf(ctx context.Context, handle string) ([]worker.Subscriber, error) {
	if w.node.worker == nil {
		return nil, nil
	}
	return w.node.worker.SubscribersOf(ctx, handle)
}

// placed runs a workload through placement, or on this node when it has none. A
// delivery is not run where it was taken: a claim is leader-only and a publish is
// wherever its publisher ran, so either would pile work onto one node.
type placed struct{ node *Instance }

func (p placed) Invoke(ctx context.Context, at specs.Address, req *invoke.Request) (*invoke.Response, error) {
	if p.node.workers == nil {
		return p.node.worker.Invoke(ctx, at, req)
	}
	return p.node.workers.Invoke(ctx, at, req)
}
