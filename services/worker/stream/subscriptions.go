package stream

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/taubyte/tau/pkg/specs"
	"github.com/taubyte/tau/services/worker"
)

// Subscriptions runs one consumer for each subscriber of a stream on a log, on every
// node holding its deployment; the log, not this node, decides which of them gets a
// message. A stream on the swarm's own engine has no consumer: the publisher's node
// delivers it. Construct it with NewSubscriptions.
type Subscriptions struct {
	invoker Invoker
	secrets Secrets
	recheck time.Duration

	ctx  context.Context
	stop context.CancelFunc

	mu      sync.Mutex
	running map[string]*consuming
}

// consuming is one subscriber's consumer.
type consuming struct {
	stop context.CancelFunc
	done chan struct{}
}

func NewSubscriptions(invoker Invoker, secrets Secrets) (*Subscriptions, error) {
	if invoker == nil || secrets == nil {
		return nil, errors.New("stream: subscriptions need an invoker and secrets")
	}
	ctx, stop := context.WithCancel(context.Background())
	return &Subscriptions{invoker: invoker, secrets: secrets, recheck: recheckEvery, ctx: ctx, stop: stop, running: map[string]*consuming{}}, nil
}

// Reconcile starts a consumer for each subscriber on a log that has none, and stops
// those whose subscriber is no longer listed or whose stream changed.
func (s *Subscriptions) Reconcile(subs []worker.Subscriber) {
	wanted := map[string]worker.Subscriber{}
	for _, sub := range subs {
		if sub.Stream.Refused != "" || engines[sub.Stream.Settings[specs.StreamKeyEngine]] == nil {
			continue
		}
		wanted[subscriptionKey(&sub)] = sub
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ctx.Err() != nil {
		return
	}
	for key, c := range s.running {
		if _, ok := wanted[key]; !ok {
			c.stop()
			<-c.done
			delete(s.running, key)
		}
	}
	for key, sub := range wanted {
		if _, ok := s.running[key]; ok {
			continue
		}
		ctx, stop := context.WithCancel(s.ctx)
		c := &consuming{stop: stop, done: make(chan struct{})}
		s.running[key] = c
		go s.consume(ctx, sub, c.done)
	}
}

// consume keeps one consumer going, dialling again after a failure, until stopped.
func (s *Subscriptions) consume(ctx context.Context, sub worker.Subscriber, done chan<- struct{}) {
	defer close(done)
	to := subscriberHandler{invoker: s.invoker, to: &sub}
	for {
		err := s.once(ctx, &sub, to)
		if ctx.Err() != nil {
			return
		}
		if errors.Is(err, errRotated) {
			continue
		}
		logger.Warnf("stream: %s consuming %s: %s", sub.Target.Name, sub.Stream.Label, err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(retryEvery):
		}
	}
}

// errRotated is a consumer that stopped to log in again with a rotated credential.
var errRotated = errors.New("stream: credential rotated")

// once runs one connection's consumer until it fails, or until its credential is
// rotated: then it drains between messages and answers errRotated.
func (s *Subscriptions) once(ctx context.Context, sub *worker.Subscriber, to handler) error {
	secret, err := credential(ctx, s.secrets, sub.Stream)
	if err != nil {
		return err
	}
	group := groupOf(sub)
	c, err := dialWith(ctx, sub.Stream, secret, group)
	if err != nil {
		return err
	}
	defer c.Close()

	drain := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- c.consume(ctx, sub.Stream.Settings[specs.StreamKeyName], group, to, drain) }()

	recheck := time.NewTicker(s.recheck)
	defer recheck.Stop()
	for {
		select {
		case err := <-done:
			return err
		case <-recheck.C:
			now, err := credential(ctx, s.secrets, sub.Stream)
			if err != nil || now == secret {
				continue
			}
			close(drain)
			if err := <-done; !errors.Is(err, errDrained) {
				return err
			}
			return errRotated
		}
	}
}

// Running is how many consumers this node runs.
func (s *Subscriptions) Running() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.running)
}

// Close stops every consumer and waits for them.
func (s *Subscriptions) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stop()
	for key, c := range s.running {
		<-c.done
		delete(s.running, key)
	}
	return nil
}

// subscriptionKey is one subscriber of one stream as it is declared now: a changed
// host or credential is a different consumer.
func subscriptionKey(sub *worker.Subscriber) string {
	t := sub.Target
	parts := []string{t.Project, t.Environment, strings.Join(t.Scope, "/"), t.Kind, t.Name, sub.Path, sub.Stream.Label, sub.Stream.Handle}
	for _, k := range slices.Sorted(maps.Keys(sub.Stream.Settings)) {
		parts = append(parts, k, sub.Stream.Settings[k])
	}
	return strings.Join(parts, "\x00")
}

// subscriberHandler invokes one subscriber with what its consumer took.
type subscriberHandler struct {
	invoker Invoker
	to      *worker.Subscriber
}

func (h subscriberHandler) handle(ctx context.Context, message []byte, id string) error {
	return invokeSubscriber(ctx, h.invoker, h.to, message, id)
}
