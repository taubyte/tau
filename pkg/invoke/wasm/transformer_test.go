package wasm_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/invoke/wasm"
)

// A real transformer that changes what it forwards, so the host is shown to read the
// forwarded request.

func shaper(t *testing.T) []byte {
	t.Helper()
	code, err := os.ReadFile("fixtures/shaper.wasm")
	require.NoError(t, err, "run `cargo component build --release` in fixtures/shaper")
	return code
}

func shaperSpec() invoke.Spec {
	return invoke.Spec{
		Name:     "redact",
		Artifact: "bafyshaper",
		Settings: map[string]string{wasm.SettingImplements: wasm.WorldGatewayTransformer},
	}
}

// transforming starts a shaper and returns something that can be driven.
func transforming(t *testing.T) invoke.Transforms {
	t.Helper()

	instance := start(t, tier(t, shaper(t)), shaperSpec())
	as, ok := instance.(invoke.Transforms)
	require.True(t, ok, "a started transformer transforms")
	return as
}

func TestATransformerForwardsAModifiedRequest(t *testing.T) {
	out, err := transforming(t).Transform(t.Context(), &invoke.Request{
		Method:  "POST",
		Path:    "/checkout",
		Headers: map[string][]string{"X-Card": {"4111"}},
		Body:    []byte("pay"),
	})
	require.NoError(t, err)
	require.Nil(t, out.Answer, "it forwarded rather than answering")
	require.NotNil(t, out.Forward)

	require.Equal(t, "POST", out.Forward.Method)
	require.Equal(t, "/checkout", out.Forward.Path)
	require.Equal(t, "shaped:pay", string(out.Forward.Body), "the body it changed, not the one it was given")
	// Canonical keys: the guest wrote `x-shaped`.
	require.Equal(t, []string{"POST:/checkout"}, out.Forward.Headers["X-Shaped"],
		"a header carrying what it saw, which is how the whole request is shown to have crossed")
	require.Equal(t, []string{"4111"}, out.Forward.Headers["X-Card"], "the headers it was sent survive")
	require.NotContains(t, out.Forward.Headers, "x-shaped", "and only under the canonical key")
}

func TestATransformerCanAnswerAndStop(t *testing.T) {
	out, err := transforming(t).Transform(t.Context(), &invoke.Request{
		Method:  "GET",
		Path:    "/admin",
		Headers: map[string][]string{"X-Shape": {"reject"}},
	})
	require.NoError(t, err)
	require.Nil(t, out.Forward, "nothing carries on")
	require.NotNil(t, out.Answer)

	require.Equal(t, 418, out.Answer.Status)
	require.Equal(t, "the chain stopped here", string(out.Answer.Body))
	require.Equal(t, []string{"shaper"}, out.Answer.Headers["x-shaped-by"])
}

func TestATransformerForwardsAnEmptyBody(t *testing.T) {
	out, err := transforming(t).Transform(t.Context(), &invoke.Request{Method: "GET", Path: "/"})
	require.NoError(t, err)
	require.NotNil(t, out.Forward)
	require.Equal(t, "shaped:", string(out.Forward.Body))
}

func TestAHandlerIsNotATransformer(t *testing.T) {
	instance := start(t, tier(t, handler(t)), invoke.Spec{Name: "checkout", Artifact: "bafy"})

	as, ok := instance.(invoke.Transforms)
	require.True(t, ok, "the type carries the method; the instance refuses it")
	_, err := as.Transform(t.Context(), &invoke.Request{})
	require.ErrorIs(t, err, wasm.ErrUnsupportedWorld)
}

func TestATransformerAnswersNoRequest(t *testing.T) {
	instance := start(t, tier(t, shaper(t)), shaperSpec())

	_, err := instance.Serve(t.Context(), &invoke.Request{Method: "GET", Path: "/"})
	require.ErrorIs(t, err, wasm.ErrUnsupportedWorld)
	require.Contains(t, err.Error(), wasm.WorldGatewayTransformer)
}

func TestStartRefusesTheDecidingWorlds(t *testing.T) {
	r := tier(t, guard(t))

	for _, world := range []string{wasm.WorldAuthorizer, wasm.WorldValidator} {
		_, err := r.Start(t.Context(), invoke.Spec{
			Name: "x", Artifact: "bafy",
			Settings: map[string]string{wasm.SettingImplements: world},
		})
		require.ErrorIs(t, err, wasm.ErrUnsupportedWorld, world)
	}
}
