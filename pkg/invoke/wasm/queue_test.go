package wasm_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/invoke/wasm"
)

// work is a queue, as this tier sees one.
type work struct {
	mu   sync.Mutex
	sent [][3]string
	err  error
}

func newWork() *work { return &work{} }

func (w *work) Send(_ context.Context, grant invoke.Grant, id string, data []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return w.err
	}
	w.sent = append(w.sent, [3]string{grant.Handle, id, string(data)})
	return nil
}

func (w *work) pushed() [][3]string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([][3]string(nil), w.sent...)
}

func queueGrant(label, handle string, ops ...string) []invoke.Grant {
	return []invoke.Grant{{
		Kind: "sync", Label: label, Handle: handle, Ops: ops,
		Settings: map[string]string{"type": "queue"},
	}}
}

// TestAGuestSendsToAQueue, with the label resolved to the handle.
func TestAComponentSendsWork(t *testing.T) {
	q := newWork()
	instance := start(t, tier(t, cacher(t), wasm.WithQueues(q)), invoke.Spec{
		Name: "fn", Reaches: queueGrant("jobs", "shop/prod/QmJobs", "send"),
	})

	res := call(t, instance, "/send/jobs/order:1", []byte("charge it"))
	require.Equal(t, 204, res.Status, string(res.Body))
	require.Equal(t, [][3]string{{"shop/prod/QmJobs", "order:1", "charge it"}}, q.pushed())
}

// TestAnIdIsTheCALLERS, which is what makes a retry safe: the same id twice is
// one item and not two deliveries of the same work.
func TestTheIdCrossesAsWritten(t *testing.T) {
	q := newWork()
	instance := start(t, tier(t, cacher(t), wasm.WithQueues(q)), invoke.Spec{
		Name: "fn", Reaches: queueGrant("jobs", "h", "send"),
	})

	require.Equal(t, 204, call(t, instance, "/send/jobs/order:1", []byte("a")).Status)
	require.Equal(t, 204, call(t, instance, "/send/jobs/order:1", []byte("b")).Status)

	sent := q.pushed()
	require.Len(t, sent, 2)
	require.Equal(t, sent[0][1], sent[1][1], "the same id, which the queue collapses")
}

func TestAnUndeclaredQueueIsRefused(t *testing.T) {
	q := newWork()
	instance := start(t, tier(t, cacher(t), wasm.WithQueues(q)), invoke.Spec{
		Name: "fn", Reaches: queueGrant("jobs", "h", "send"),
	})

	res := call(t, instance, "/send/somebody-elses/1", []byte("x"))
	require.Equal(t, 403, res.Status)
	require.Equal(t, "no-such-queue:somebody-elses", string(res.Body))
	require.Empty(t, q.pushed())
}

// TestReceiveBuysNothing. It is a DELIVERY — the platform claims an item and
// invokes a workload — so it needs a dispatcher and not a host function.
func TestAGrantForReceiveDoesNotSend(t *testing.T) {
	q := newWork()
	instance := start(t, tier(t, cacher(t), wasm.WithQueues(q)), invoke.Spec{
		Name: "fn", Reaches: queueGrant("jobs", "h", "receive"),
	})

	res := call(t, instance, "/send/jobs/1", []byte("x"))
	require.Equal(t, 403, res.Status)
	require.Equal(t, "denied:jobs cannot send", string(res.Body))
	require.Empty(t, q.pushed())
}

// TestNotWritableIsItsOwnArm, because a caller RETRIES that and nothing about
// the request was wrong — which is a different instruction from an outage.
func TestAWriteOffTheLeaderTellsTheCallerToRetry(t *testing.T) {
	q := newWork()
	q.err = fmt.Errorf("%w: node is a follower", wasm.ErrNotWritable)

	instance := start(t, tier(t, cacher(t), wasm.WithQueues(q)), invoke.Spec{
		Name: "fn", Reaches: queueGrant("jobs", "h", "send"),
	})

	res := call(t, instance, "/send/jobs/1", []byte("x"))
	require.Equal(t, 403, res.Status)
	require.Contains(t, string(res.Body), "not-writable:")
	require.Contains(t, string(res.Body), "follower")
}

func TestAQueueFailureIsUnavailable(t *testing.T) {
	q := newWork()
	q.err = errors.New("the swarm is unreachable")

	instance := start(t, tier(t, cacher(t), wasm.WithQueues(q)), invoke.Spec{
		Name: "fn", Reaches: queueGrant("jobs", "h", "send"),
	})

	res := call(t, instance, "/send/jobs/1", []byte("x"))
	require.Equal(t, 403, res.Status)
	require.Contains(t, string(res.Body), "unavailable:")
}

func TestATierWithNoQueuesRefusesAWorkloadThatSends(t *testing.T) {
	_, err := tier(t, cacher(t)).Start(t.Context(), invoke.Spec{
		Name: "fn", Artifact: "bafy", Reaches: queueGrant("jobs", "h", "send"),
	})
	require.ErrorIs(t, err, wasm.ErrNoQueues)
}
