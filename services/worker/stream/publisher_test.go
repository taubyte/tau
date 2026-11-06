package stream

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/specs"
	"github.com/taubyte/tau/services/worker"
)

// subscribed is the worker's answer, fixed.
type subscribed map[string][]worker.Subscriber

func (s subscribed) SubscribersOf(_ context.Context, handle string) ([]worker.Subscriber, error) {
	if subs, ok := s["error"]; ok && len(subs) == 0 {
		return nil, errors.New("no such deployment")
	}
	return s[handle], nil
}

// call is one invocation a subscriber received.
type call struct {
	at       specs.Address
	path     string
	label    string
	delivery string
	body     string
}

// recorder is placement: it records what it was asked to run, and fails the first
// failures calls, or with failing set the first failures calls carrying that body.
type recorder struct {
	mu       sync.Mutex
	calls    []call
	failures int
	failing  string
	status   int
}

func (r *recorder) Invoke(_ context.Context, at specs.Address, req *invoke.Request) (*invoke.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, call{
		at: at, path: req.Path, body: string(req.Body),
		label:    first(req.Headers[headerStream]),
		delivery: first(req.Headers[headerDelivery]),
	})
	if r.failures > 0 && (r.failing == "" || r.failing == string(req.Body)) {
		r.failures--
		return nil, errors.New("the node went away")
	}
	if r.status != 0 {
		return &invoke.Response{Status: r.status}, nil
	}
	return &invoke.Response{Status: http.StatusNoContent}, nil
}

func (r *recorder) received() []call {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]call(nil), r.calls...)
}

// bodiesFor is what one subscriber received, in order.
func (r *recorder) bodiesFor(name string) []string {
	var out []string
	for _, c := range r.received() {
		if c.at.Name == name {
			out = append(out, c.body)
		}
	}
	return out
}

func first(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

// vault answers every credential with one secret.
type vault struct{ secret string }

func (v vault) Secret(_ context.Context, _, _, id string) (string, error) {
	if v.secret == "" {
		return "", errors.New("no secret " + id)
	}
	return v.secret, nil
}

const ordersHandle = "shop/prod/QmOrders"

func subscriber(name string, settings map[string]string) worker.Subscriber {
	return worker.Subscriber{
		Target: specs.Address{Project: "shop", Environment: "prod", Kind: "function", Name: name},
		ID:     "Qm" + name,
		Stream: invoke.Grant{Kind: "stream", Label: "orders", Handle: ordersHandle, Ops: []string{"subscribe"}, Settings: settings},
		Path:   "/" + name,
	}
}

func distributed() map[string]string {
	return map[string]string{specs.StreamKeyEngine: specs.StreamEngineDistributed, specs.StreamKeyName: "orders"}
}

func publisherOver(t *testing.T, subs subscribed, to Invoker) *Publisher {
	t.Helper()
	p, err := NewPublisher(subs, to, vault{secret: "tau:s3cret"})
	require.NoError(t, err)
	t.Cleanup(func() { p.Close() })
	return p
}

func TestAPublisherNeedsItsThreeSeams(t *testing.T) {
	_, err := NewPublisher(nil, &recorder{}, vault{})
	require.Error(t, err)
	_, err = NewSubscriptions(nil, vault{})
	require.Error(t, err)
}

func TestADistributedMessageReachesEverySubscriberOnce(t *testing.T) {
	refused := subscriber("ledger", distributed())
	refused.Stream.Refused = "not here"
	to := &recorder{}
	p := publisherOver(t, subscribed{ordersHandle: {subscriber("mailer", distributed()), subscriber("audit", distributed()), refused}}, to)

	message := []byte("order:1")
	require.NoError(t, p.Publish(t.Context(), invoke.Grant{Handle: ordersHandle, Settings: distributed()}, message))
	copy(message, "changed")

	require.Eventually(t, func() bool { return len(to.received()) == 2 }, 5*time.Second, 10*time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	calls := to.received()
	require.Len(t, calls, 2, "once each, and never the refused one")
	for _, c := range calls {
		require.Equal(t, "order:1", c.body, "the message as published, not the guest's memory after")
		require.Equal(t, "/"+c.at.Name, c.path)
		require.Equal(t, "orders", c.label)
		require.Empty(t, c.delivery, "the swarm keeps nothing to point at")
	}
}

func TestAMessageNobodySubscribesToReachesNobody(t *testing.T) {
	to := &recorder{}
	p := publisherOver(t, subscribed{}, to)
	require.NoError(t, p.Publish(t.Context(), invoke.Grant{Handle: ordersHandle, Settings: distributed()}, []byte("x")))
	time.Sleep(50 * time.Millisecond)
	require.Empty(t, to.received())
}

func TestAFailedDeliveryIsNotRetriedOnTheSwarm(t *testing.T) {
	to := &recorder{status: http.StatusInternalServerError}
	p := publisherOver(t, subscribed{ordersHandle: {subscriber("mailer", distributed())}}, to)
	require.NoError(t, p.Publish(t.Context(), invoke.Grant{Handle: ordersHandle, Settings: distributed()}, []byte("x")))
	require.Eventually(t, func() bool { return len(to.received()) == 1 }, 5*time.Second, 10*time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	require.Len(t, to.received(), 1, "at most once")
}

func TestAPublishTheWorkerCannotPlaceIsAnError(t *testing.T) {
	p := publisherOver(t, subscribed{"error": nil}, &recorder{})
	require.Error(t, p.Publish(t.Context(), invoke.Grant{Handle: ordersHandle, Settings: distributed()}, []byte("x")))
}

func TestAPublishWaitsForRoomAndGivesUpWithItsCaller(t *testing.T) {
	subs := make([]worker.Subscriber, pending+deliverers+1)
	for i := range subs {
		subs[i] = subscriber("mailer", distributed())
	}
	blocked := &blocking{release: make(chan struct{})}
	p := publisherOver(t, subscribed{ordersHandle: subs}, blocked)
	t.Cleanup(func() { close(blocked.release) })

	ctx, stop := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer stop()
	err := p.Publish(ctx, invoke.Grant{Handle: ordersHandle, Settings: distributed()}, []byte("x"))
	require.ErrorIs(t, err, context.DeadlineExceeded, "every deliverer busy and the queue full")
}

// blocking is a subscriber that never answers until released.
type blocking struct{ release chan struct{} }

func (b *blocking) Invoke(ctx context.Context, _ specs.Address, _ *invoke.Request) (*invoke.Response, error) {
	select {
	case <-b.release:
	case <-ctx.Done():
	}
	return &invoke.Response{Status: http.StatusNoContent}, nil
}

func TestAClosedPublisherRefuses(t *testing.T) {
	p := publisherOver(t, subscribed{ordersHandle: {subscriber("mailer", distributed())}}, &recorder{})
	require.NoError(t, p.Close())
	require.NoError(t, p.Close(), "Close is idempotent")
	err := p.Publish(t.Context(), invoke.Grant{Handle: ordersHandle, Settings: distributed()}, []byte("x"))
	require.ErrorIs(t, err, ErrClosed)
}

func TestAnEngineWithNoDriverIsRefusedByName(t *testing.T) {
	p := publisherOver(t, subscribed{}, &recorder{})
	err := p.Publish(t.Context(), invoke.Grant{Handle: ordersHandle, Settings: map[string]string{specs.StreamKeyEngine: "rabbitmq"}}, []byte("x"))
	require.ErrorIs(t, err, ErrUnknownEngine)
	require.ErrorContains(t, err, "rabbitmq")
}
