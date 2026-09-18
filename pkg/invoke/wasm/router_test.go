package wasm_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/invoke/wasm"
)

// A REAL ROUTER. The fixture ECHOES the path it was given into the destination
// it returns, which is what separates "the arms work" from "the request
// crossed" — a router that ignored its argument and returned a constant would
// satisfy every assertion about which arm was taken.

func picker(t *testing.T) []byte {
	t.Helper()
	code, err := os.ReadFile("fixtures/picker.wasm")
	require.NoError(t, err, "run `cargo component build --release` in fixtures/picker")
	return code
}

func pickerSpec() invoke.Spec {
	return invoke.Spec{
		Name:     "edge_router",
		Artifact: "bafypicker",
		Settings: map[string]string{wasm.SettingImplements: wasm.WorldGatewayRouter},
	}
}

func routing(t *testing.T) invoke.Routes {
	t.Helper()

	instance := start(t, tier(t, picker(t)), pickerSpec())
	as, ok := instance.(invoke.Routes)
	require.True(t, ok, "a started router routes")
	return as
}

// TestAROUTERAnswersWithAnAddressAndTheRequestReachedIt.
func TestARouterPicksADestination(t *testing.T) {
	out, err := routing(t).Route(t.Context(), &invoke.Request{Method: "GET", Path: "/orders/42"})
	require.NoError(t, err)
	require.Nil(t, out.Answer)
	require.Empty(t, out.None)
	require.NotNil(t, out.To)

	require.Equal(t, "function", out.To.Kind)
	require.Empty(t, out.To.Namespace, "an empty namespace means the gateway's own scope")
	require.Equal(t, "saw_orders_42", out.To.Name, "the path it was handed, echoed back")
}

// TestAROUTERMayNameANamespace, which is how one gateway reaches a workload in
// a namespace it does not live in.
func TestARouterCanNameANamespace(t *testing.T) {
	out, err := routing(t).Route(t.Context(), &invoke.Request{Method: "GET", Path: "/shop/cart"})
	require.NoError(t, err)
	require.Equal(t, "/shop", out.To.Namespace)
	require.Equal(t, "checkout", out.To.Name)
}

// TestAROUTERMayANSWERItself. A redirect is the obvious case: it needs no
// workload, and making the gateway invoke one to produce a 302 would be a cold
// start for a header.
func TestARouterCanAnswerItself(t *testing.T) {
	out, err := routing(t).Route(t.Context(), &invoke.Request{Method: "GET", Path: "/answer"})
	require.NoError(t, err)
	require.Nil(t, out.To)
	require.Equal(t, 302, out.Answer.Status)
	require.Equal(t, []string{"/elsewhere"}, out.Answer.Headers["location"])
}

// TestNONEIsADecisionAndNotAFailure. The router ran, understood the request and
// says nothing here handles it — which is a 404. A router that could not be run
// is a different thing, because a routing table that cannot be read is not the
// same as a path that is not in it.
func TestARouterThatMatchesNothingSaysSo(t *testing.T) {
	out, err := routing(t).Route(t.Context(), &invoke.Request{Method: "GET", Path: "/nothing"})
	require.NoError(t, err, "deciding that nothing handles it is an answer")
	require.Nil(t, out.To)
	require.Nil(t, out.Answer)
	require.Equal(t, "no rule matched", out.None)
}

// ── the world gate ───────────────────────────────────────────────────────

func TestATransformerIsNotARouter(t *testing.T) {
	instance := start(t, tier(t, shaper(t)), shaperSpec())

	as, ok := instance.(invoke.Routes)
	require.True(t, ok)
	_, err := as.Route(t.Context(), &invoke.Request{})
	require.ErrorIs(t, err, wasm.ErrUnsupportedWorld)
	require.Contains(t, err.Error(), wasm.WorldGatewayTransformer)
}

func TestARouterIsNotATransformer(t *testing.T) {
	instance := start(t, tier(t, picker(t)), pickerSpec())

	as, ok := instance.(invoke.Transforms)
	require.True(t, ok)
	_, err := as.Transform(t.Context(), &invoke.Request{})
	require.ErrorIs(t, err, wasm.ErrUnsupportedWorld)
	require.Contains(t, err.Error(), wasm.WorldGatewayRouter)
}

func TestARouterAnswersNoRequest(t *testing.T) {
	instance := start(t, tier(t, picker(t)), pickerSpec())

	_, err := instance.Serve(t.Context(), &invoke.Request{Method: "GET", Path: "/"})
	require.ErrorIs(t, err, wasm.ErrUnsupportedWorld)
}
