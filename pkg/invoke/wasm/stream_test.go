package wasm_test

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/invoke/wasm"
)

// The real component: "/publish/<stream>/" with the body as message, answering the
// arm's name.

// publisher records what was published, by handle.
type publisher struct {
	mu   sync.Mutex
	sent map[string][][]byte
	err  error
}

func newPublisher() *publisher { return &publisher{sent: map[string][][]byte{}} }

func (p *publisher) Publish(_ context.Context, grant invoke.Grant, message []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return p.err
	}
	p.sent[grant.Handle] = append(p.sent[grant.Handle], bytes.Clone(message))
	return nil
}

func (p *publisher) messages(handle string) [][]byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sent[handle]
}

func streamGrant(label, handle string, ops ...string) []invoke.Grant {
	return []invoke.Grant{{Kind: "stream", Label: label, Handle: handle, Ops: ops}}
}

func TestAComponentPublishes(t *testing.T) {
	sent := newPublisher()
	instance := start(t, tier(t, cacher(t), wasm.WithStreams(sent)), invoke.Spec{
		Name:    "shop/prod/function/checkout",
		Reaches: streamGrant("orders", "shop/prod/QmOrders", "publish"),
	})

	res := call(t, instance, "/publish/orders/", []byte("order:1"))
	require.Equal(t, 204, res.Status, string(res.Body))
	require.Equal(t, [][]byte{[]byte("order:1")}, sent.messages("shop/prod/QmOrders"),
		"the label resolved to the handle")
}

func TestAnEmptyMessageIsStillAMessage(t *testing.T) {
	sent := newPublisher()
	instance := start(t, tier(t, cacher(t), wasm.WithStreams(sent)), invoke.Spec{
		Name: "fn", Reaches: streamGrant("orders", "h", "publish"),
	})

	require.Equal(t, 204, call(t, instance, "/publish/orders/", nil).Status)
	require.Len(t, sent.messages("h"), 1)
	require.Empty(t, sent.messages("h")[0])
}

func TestAnUndeclaredStreamIsRefused(t *testing.T) {
	sent := newPublisher()
	instance := start(t, tier(t, cacher(t), wasm.WithStreams(sent)), invoke.Spec{
		Name: "fn", Reaches: streamGrant("orders", "h", "publish"),
	})

	res := call(t, instance, "/publish/somebody-elses/", []byte("x"))
	require.Equal(t, 403, res.Status)
	require.Equal(t, "no-such-stream:somebody-elses", string(res.Body))
	require.Empty(t, sent.messages("h"), "nothing was sent")
}

func TestAGrantForSubscribeDoesNotAllowPublish(t *testing.T) {
	sent := newPublisher()
	instance := start(t, tier(t, cacher(t), wasm.WithStreams(sent)), invoke.Spec{
		Name: "fn", Reaches: streamGrant("orders", "h", "subscribe"),
	})

	res := call(t, instance, "/publish/orders/", []byte("x"))
	require.Equal(t, 403, res.Status)
	require.Equal(t, "denied:orders cannot publish", string(res.Body))
	require.Empty(t, sent.messages("h"))
}

func TestARefusedStreamCarriesItsReason(t *testing.T) {
	sent := newPublisher()
	instance := start(t, tier(t, cacher(t), wasm.WithStreams(sent)), invoke.Spec{
		Name: "fn",
		Reaches: []invoke.Grant{{
			Kind: "stream", Label: "orders", Handle: "h", Ops: []string{"publish"},
			Refused: `stream "orders" is on the "kafka" engine, and this cloud has no driver for it`,
		}},
	})

	res := call(t, instance, "/publish/orders/", []byte("x"))
	require.Equal(t, 403, res.Status)
	require.Contains(t, string(res.Body), "kafka")
	require.Empty(t, sent.messages("h"))
}

func TestAGrantOfAnotherKindDoesNotPublish(t *testing.T) {
	sent := newPublisher()
	instance := start(t, tier(t, cacher(t), wasm.WithCaches(newCaches("h")), wasm.WithStreams(sent)),
		invoke.Spec{
			Name: "fn",
			Reaches: []invoke.Grant{
				{Kind: "cache", Label: "orders", Handle: "h", Ops: []string{"get", "put"}},
			},
		})

	res := call(t, instance, "/publish/orders/", []byte("x"))
	require.Equal(t, 403, res.Status)
	require.Equal(t, "no-such-stream:orders", string(res.Body))
}

func TestATierWithNoStreamsRefusesAWorkloadThatPublishes(t *testing.T) {
	_, err := tier(t, cacher(t)).Start(t.Context(), invoke.Spec{
		Name: "fn", Artifact: "bafy", Reaches: streamGrant("orders", "h", "publish"),
	})
	require.ErrorIs(t, err, wasm.ErrNoStreams)
}

func TestATierWithNoStreamsRunsAWorkloadThatPublishesToNothing(t *testing.T) {
	instance := start(t, tier(t, cacher(t)), invoke.Spec{Name: "fn"})

	res := call(t, instance, "/publish/orders/", []byte("x"))
	require.Equal(t, 403, res.Status)
	require.Equal(t, "no-such-stream:orders", string(res.Body))

	// Still alive, which is the half a trap would have taken.
	require.Equal(t, 403, call(t, instance, "/publish/other/", nil).Status)
}

func TestAPublishFailureIsUnavailable(t *testing.T) {
	sent := newPublisher()
	sent.err = errors.New("the swarm is unreachable")

	instance := start(t, tier(t, cacher(t), wasm.WithStreams(sent)), invoke.Spec{
		Name: "fn", Reaches: streamGrant("orders", "h", "publish"),
	})

	res := call(t, instance, "/publish/orders/", []byte("x"))
	require.Equal(t, 403, res.Status)
	require.Contains(t, string(res.Body), "unavailable:")
	require.Contains(t, string(res.Body), "the swarm is unreachable")
}

func TestACacheAndAStreamInOneWorkload(t *testing.T) {
	backing := newCaches("QmCache")
	sent := newPublisher()

	instance := start(t, tier(t, cacher(t), wasm.WithCaches(backing), wasm.WithStreams(sent)),
		invoke.Spec{
			Name: "fn",
			Reaches: []invoke.Grant{
				{Kind: "cache", Label: "sessions", Handle: "QmCache", Ops: []string{"get", "put"}},
				{Kind: "stream", Label: "orders", Handle: "QmOrders", Ops: []string{"publish"}},
			},
		})

	require.Equal(t, 200, call(t, instance, "/roundtrip/sessions/k", []byte("v")).Status)
	require.Equal(t, 204, call(t, instance, "/publish/orders/", []byte("m")).Status)
	require.Len(t, sent.messages("QmOrders"), 1)
}

func busGrant(label, handle string, ops ...string) []invoke.Grant {
	return []invoke.Grant{{Kind: "bus", Label: label, Handle: handle, Ops: ops}}
}

func TestAComponentPublishesToABus(t *testing.T) {
	sent := newPublisher()
	instance := start(t, tier(t, cacher(t), wasm.WithBus(sent)), invoke.Spec{
		Name: "fn", Reaches: busGrant("chat", "shop/prod/QmChat", "publish"),
	})

	res := call(t, instance, "/broadcast/chat/", []byte("hello"))
	require.Equal(t, 204, res.Status, string(res.Body))
	require.Equal(t, [][]byte{[]byte("hello")}, sent.messages("shop/prod/QmChat"))
}

func TestABusGrantIsNotAStreamGrant(t *testing.T) {
	streams := newPublisher()
	buses := newPublisher()

	instance := start(t, tier(t, cacher(t), wasm.WithStreams(streams), wasm.WithBus(buses)),
		invoke.Spec{
			Name: "fn",
			Reaches: []invoke.Grant{
				{Kind: "stream", Label: "orders", Handle: "QmStream", Ops: []string{"publish"}},
				{Kind: "bus", Label: "orders", Handle: "QmBus", Ops: []string{"publish"}},
			},
		})

	require.Equal(t, 204, call(t, instance, "/publish/orders/", []byte("to the stream")).Status)
	require.Equal(t, 204, call(t, instance, "/broadcast/orders/", []byte("to the bus")).Status)

	require.Equal(t, [][]byte{[]byte("to the stream")}, streams.messages("QmStream"))
	require.Equal(t, [][]byte{[]byte("to the bus")}, buses.messages("QmBus"))
}

func TestAnUndeclaredBusIsRefused(t *testing.T) {
	sent := newPublisher()
	instance := start(t, tier(t, cacher(t), wasm.WithBus(sent)), invoke.Spec{
		Name: "fn", Reaches: busGrant("chat", "h", "publish"),
	})

	res := call(t, instance, "/broadcast/somebody-elses/", []byte("x"))
	require.Equal(t, 403, res.Status)
	require.Equal(t, "no-such-bus:somebody-elses", string(res.Body))
	require.Empty(t, sent.messages("h"))
}

func TestAGrantForBusSubscribeDoesNotPublish(t *testing.T) {
	sent := newPublisher()
	instance := start(t, tier(t, cacher(t), wasm.WithBus(sent)), invoke.Spec{
		Name: "fn", Reaches: busGrant("chat", "h", "subscribe"),
	})

	res := call(t, instance, "/broadcast/chat/", []byte("x"))
	require.Equal(t, 403, res.Status)
	require.Equal(t, "denied:chat cannot publish", string(res.Body))
}

func TestATierWithNoBusRefusesAWorkloadThatPublishesToOne(t *testing.T) {
	_, err := tier(t, cacher(t)).Start(t.Context(), invoke.Spec{
		Name: "fn", Artifact: "bafy", Reaches: busGrant("chat", "h", "publish"),
	})
	require.ErrorIs(t, err, wasm.ErrNoBus)
}
