package worker

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/specs"
)

func TestAnUnheldDeploymentHasNoSubscribers(t *testing.T) {
	h := newHarness(t)
	require.Empty(t, h.Worker.Subscribers(t.Context()))
}

func TestASubscriberIsFoundWithItsStreamsEngine(t *testing.T) {
	h := newHarness(t)
	holding(t, h, "function", "checkout")

	subs := h.Worker.Subscribers(t.Context())
	require.Len(t, subs, 1, "notify is the fixture's one subscriber")
	got := subs[0]

	require.Equal(t, "notify", got.Target.Name)
	require.Equal(t, []string{"shop"}, got.Target.Scope)
	require.NotEmpty(t, got.ID)
	require.Equal(t, "orders", got.Stream.Label)
	require.Equal(t, "/", got.Path, "a wasm subscriber declares no path and is called at the root")
	require.True(t, got.Stream.Allows(subscribeOp))
	require.Equal(t, specs.StreamEngineDistributed, got.Stream.Settings[specs.StreamKeyEngine])
	require.Equal(t, "orders", got.Stream.Settings[specs.StreamKeyName])
	require.Empty(t, got.Stream.Refused)

	// What a publish asks: the subscribers of one handle, the same slice while the
	// deployment's object is the same.
	of, err := h.Worker.SubscribersOf(t.Context(), got.Stream.Handle)
	require.NoError(t, err)
	require.Equal(t, subs, of)
	again, err := h.Worker.SubscribersOf(t.Context(), got.Stream.Handle)
	require.NoError(t, err)
	require.Same(t, &of[0], &again[0], "worked out once per object")
}

func TestAStreamNobodySubscribesToHasNoSubscribers(t *testing.T) {
	h := newHarness(t)
	holding(t, h, "function", "checkout")

	of, err := h.Worker.SubscribersOf(t.Context(), "shop/prod/QmNobodyListens")
	require.NoError(t, err)
	require.Empty(t, of)
}

func TestSubscribersOfAnUnknownDeploymentIsAnError(t *testing.T) {
	h := newHarness(t)
	_, err := h.Worker.SubscribersOf(t.Context(), "nowhere/prod/QmX")
	require.Error(t, err)
}

func TestAKafkaStreamCarriesWhatAConsumerNeeds(t *testing.T) {
	got := streamSettings(specs.Resource{
		"engine": "kafka", "name": "shipments", "host": "b:9092", "tls": "require", "sasl": "plain",
		"credentials": map[string]any{"name": "QmSecret", "namespace": "/"},
	})
	require.Equal(t, map[string]string{
		specs.StreamKeyEngine: "kafka", specs.StreamKeyName: "shipments", specs.StreamKeyHost: "b:9092",
		specs.StreamKeyTls: "require", specs.StreamKeySasl: "plain",
		specs.StreamCredentialsKeyName: "QmSecret", specs.StreamCredentialsKeyNamespace: "/",
	}, got)
}
