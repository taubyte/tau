package stream

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/ipfs/go-log/v2"

	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
	"github.com/taubyte/tau/pkg/specs"
	"github.com/taubyte/tau/services/worker"
)

var logger = log.Logger("tau.v2.stream")

// Subscribers is who subscribes to a stream, as the worker resolved them.
type Subscribers interface {
	SubscribersOf(ctx context.Context, handle string) ([]worker.Subscriber, error)
}

// Invoker runs a workload wherever placement puts it.
type Invoker interface {
	Invoke(ctx context.Context, at specs.Address, req *invoke.Request) (*invoke.Response, error)
}

// Secrets reads the secret a log's credentials were declared as.
type Secrets interface {
	Secret(ctx context.Context, project, environment, secretID string) (string, error)
}

// ErrClosed is a publish on a node that is shutting down.
var ErrClosed = errors.New("stream: closed")

// ErrUnknownEngine is a grant naming an engine this cloud has no driver for.
var ErrUnknownEngine = errors.New("stream: no driver for that engine")

const (
	// deliverers is how many fan-out invocations one node runs at once.
	deliverers = 32

	// pending is how many fan-out invocations may wait for a deliverer. A publish
	// past it waits, which is the backpressure a publisher feels.
	pending = 1024

	// deliverWithin bounds one invocation of a subscriber.
	deliverWithin = time.Minute
)

// What a subscriber is told about a message: the stream as its own access block
// labels it, since a wasm subscriber is called at "/" and needs another way to tell
// two streams apart, and on a log where the message sits in it.
const (
	headerStream   = "X-Taubyte-Stream"
	headerDelivery = "X-Taubyte-Delivery"
)

// Publisher backs `taubyte:stream/publisher`. The engine a grant names decides where
// a message goes: on the swarm's own, this node invokes every subscriber once and
// keeps nothing; on a log, the message is written to it and the subscribers'
// consumers (Subscriptions) deliver it. Construct it with NewPublisher.
type Publisher struct {
	subscribers Subscribers
	invoker     Invoker
	logs        *logs

	deliveries chan delivery
	closing    chan struct{}
	stop       context.CancelFunc
	running    sync.WaitGroup
	closeOnce  sync.Once
}

var _ wasmtier.Streams = (*Publisher)(nil)

// delivery is one message for one subscriber. to points into a slice the worker
// never changes once handed out.
type delivery struct {
	to      *worker.Subscriber
	message []byte
}

// NewPublisher starts the deliverers. Close stops them.
func NewPublisher(subscribers Subscribers, invoker Invoker, secrets Secrets) (*Publisher, error) {
	if subscribers == nil || invoker == nil || secrets == nil {
		return nil, errors.New("stream: a publisher needs subscribers, an invoker and secrets")
	}
	ctx, stop := context.WithCancel(context.Background())
	p := &Publisher{
		subscribers: subscribers,
		invoker:     invoker,
		logs:        newLogs(secrets),
		deliveries:  make(chan delivery, pending),
		closing:     make(chan struct{}),
		stop:        stop,
	}
	p.running.Add(deliverers)
	for range deliverers {
		go p.deliver(ctx)
	}
	return p, nil
}

func (p *Publisher) Publish(ctx context.Context, grant invoke.Grant, message []byte) error {
	switch engine := grant.Settings[specs.StreamKeyEngine]; engine {
	case specs.StreamEngineDistributed:
		return p.fanOut(ctx, grant.Handle, message)
	case specs.StreamEngineKafka, specs.StreamEngineNats:
		return p.logs.publish(ctx, grant, message)
	default:
		return fmt.Errorf("%w: %q", ErrUnknownEngine, engine)
	}
}

// fanOut hands the message to every subscriber's deliverer and returns: a subscriber
// is invoked once, and a message nobody subscribes to reaches nobody.
func (p *Publisher) fanOut(ctx context.Context, handle string, message []byte) error {
	select {
	case <-p.closing:
		return ErrClosed
	default:
	}
	subs, err := p.subscribers.SubscribersOf(ctx, handle)
	if err != nil {
		return err
	}
	if len(subs) == 0 {
		return nil
	}

	// The guest's bytes are only valid for the call; one copy serves every subscriber.
	message = bytes.Clone(message)
	for i := range subs {
		if subs[i].Stream.Refused != "" {
			continue
		}
		select {
		case p.deliveries <- delivery{to: &subs[i], message: message}:
		case <-ctx.Done():
			return ctx.Err()
		case <-p.closing:
			return ErrClosed
		}
	}
	return nil
}

func (p *Publisher) deliver(ctx context.Context) {
	defer p.running.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case d := <-p.deliveries:
			if err := invokeSubscriber(ctx, p.invoker, d.to, d.message, ""); err != nil {
				logger.Warnf("stream: %s on %s: %s", d.to.Target.Name, d.to.Stream.Label, err)
			}
		}
	}
}

// invokeSubscriber posts one message to one subscriber; anything but a 2xx is a failure.
func invokeSubscriber(ctx context.Context, invoker Invoker, to *worker.Subscriber, message []byte, deliveryID string) error {
	runCtx, stop := context.WithTimeout(ctx, deliverWithin)
	defer stop()

	headers := map[string][]string{headerStream: {to.Stream.Label}}
	if deliveryID != "" {
		headers[headerDelivery] = []string{deliveryID}
	}
	res, err := invoker.Invoke(runCtx, to.Target, &invoke.Request{
		Method:  http.MethodPost,
		Path:    to.Path,
		Headers: headers,
		Body:    message,
	})
	if err != nil {
		return err
	}
	if res.Status < 200 || res.Status > 299 {
		return fmt.Errorf("answered %d: %.200s", res.Status, res.Body)
	}
	return nil
}

// Close stops the deliverers, dropping what is still pending, and closes every
// connection to a log.
func (p *Publisher) Close() error {
	p.closeOnce.Do(func() {
		close(p.closing)
		p.stop()
		p.running.Wait()
	})
	return p.logs.Close()
}
