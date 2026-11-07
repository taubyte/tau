package wasm_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/invoke/wasm"
)

// A real proxy router answering from what it was shown (the proxy's name and the
// first four bytes), so a constant answer would fail.

func usher(t *testing.T) []byte {
	t.Helper()
	code, err := os.ReadFile("fixtures/usher.wasm")
	require.NoError(t, err, "run `cargo component build --release` in fixtures/usher")
	return code
}

func directing(t *testing.T) invoke.Directs {
	t.Helper()

	instance := start(t, tier(t, usher(t)), invoke.Spec{
		Name:     "edge_usher",
		Artifact: "bafyusher",
		Settings: map[string]string{wasm.SettingImplements: wasm.WorldProxyRouter},
	})
	as, ok := instance.(invoke.Directs)
	require.True(t, ok, "a started proxy router directs")
	return as
}

func pgConn() *invoke.Connection {
	return &invoke.Connection{
		Source:    "203.0.113.7:51000",
		Host:      "db.shop.example.com",
		Protocol:  "postgres",
		Transport: "tcp",
	}
}

func TestAProxyRouterAsksForMoreBytes(t *testing.T) {
	out, err := directing(t).Direct(t.Context(), pgConn(), []byte("pg"))
	require.NoError(t, err)
	require.Nil(t, out.To)
	require.Empty(t, out.Refused)
	require.Equal(t, uint32(4), out.More)

	require.False(t, out.Enough(2), "it has not been shown what it asked for")
	require.True(t, out.Enough(4), "and it has once four bytes have arrived")
}

func TestAProxyRouterIsCalledWithNoBytesAtAll(t *testing.T) {
	out, err := directing(t).Direct(t.Context(), pgConn(), nil)
	require.NoError(t, err)
	require.Equal(t, uint32(4), out.More)
}

func TestAProxyRouterPicksADestinationAndANamedPort(t *testing.T) {
	out, err := directing(t).Direct(t.Context(), pgConn(), []byte("pgwire-startup"))
	require.NoError(t, err)
	require.NotNil(t, out.To)

	require.Equal(t, "service", out.To.Kind)
	require.Equal(t, "/shop", out.To.Namespace)
	require.Equal(t, "db", out.To.Name)
	require.Equal(t, "pg", out.To.Port, "which of the workload's ports to stream to")
}

func TestAProxyRouterSeesTheConnectionAndTheBytes(t *testing.T) {
	out, err := directing(t).Direct(t.Context(), pgConn(), []byte("heyo there"))
	require.NoError(t, err)
	require.NotNil(t, out.To)

	require.Equal(t, "saw-db_shop_example_com-heyo", out.To.Name)
	require.Empty(t, out.To.Namespace, "an empty namespace means the proxy's own scope")
}

func TestAProxyRouterCanRefuseAConnection(t *testing.T) {
	out, err := directing(t).Direct(t.Context(), pgConn(), []byte("nope, not you"))
	require.NoError(t, err)
	require.Nil(t, out.To)
	require.Zero(t, out.More)
	require.Equal(t, "203.0.113.7:51000 is not welcome", out.Refused,
		"and it answers from the source it was told about")
}

func TestOnlyAProxyRouterDirects(t *testing.T) {
	instance := start(t, tier(t, handler(t)), invoke.Spec{
		Name: "not_a_router", Artifact: "bafyhandler",
	})

	as, ok := instance.(invoke.Directs)
	require.True(t, ok, "the tier's instance carries the method")

	_, err := as.Direct(t.Context(), pgConn(), []byte("anything"))
	require.ErrorIs(t, err, wasm.ErrUnsupportedWorld)
	require.ErrorContains(t, err, "directs nothing")
}

func TestAProxyRouterShapesNothing(t *testing.T) {
	instance := start(t, tier(t, usher(t)), invoke.Spec{
		Name:     "edge_usher",
		Artifact: "bafyusher",
		Settings: map[string]string{wasm.SettingImplements: wasm.WorldProxyRouter},
	})

	as, ok := instance.(invoke.Shapes)
	require.True(t, ok)

	_, err := as.Shape(t.Context(), &invoke.Stream{Conn: pgConn()})
	require.ErrorIs(t, err, wasm.ErrUnsupportedWorld)
	require.ErrorContains(t, err, "shapes nothing")
}
