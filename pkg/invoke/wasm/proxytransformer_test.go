package wasm_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/invoke/wasm"
)

// A real transformer that buffers until a newline, so the host is shown to carry
// state back.

func sieve(t *testing.T) []byte {
	t.Helper()
	code, err := os.ReadFile("fixtures/sieve.wasm")
	require.NoError(t, err, "run `cargo component build --release` in fixtures/sieve")
	return code
}

func shaping(t *testing.T) invoke.Shapes {
	t.Helper()

	instance := start(t, tier(t, sieve(t)), invoke.Spec{
		Name:     "edge_sieve",
		Artifact: "bafysieve",
		Settings: map[string]string{wasm.SettingImplements: wasm.WorldProxyTransformer},
	})
	as, ok := instance.(invoke.Shapes)
	require.True(t, ok, "a started proxy transformer shapes")
	return as
}

func chunk(state []byte, way invoke.Way, body string, last bool) *invoke.Stream {
	return &invoke.Stream{Conn: pgConn(), Way: way, State: state, Chunk: []byte(body), Last: last}
}

func TestATransformerBuffersAcrossChunksThroughTheHost(t *testing.T) {
	sh := shaping(t)

	first, err := sh.Shape(t.Context(), chunk(nil, invoke.Inbound, "sel", false))
	require.NoError(t, err)
	require.NotNil(t, first.Pass)
	require.Empty(t, first.Pass.Out, "nothing goes on until the line is whole")
	require.Equal(t, "sel", string(first.Pass.State), "and what it is holding comes back out")

	second, err := sh.Shape(t.Context(), chunk(first.Pass.State, invoke.Inbound, "ect 1\nse", false))
	require.NoError(t, err)
	require.Equal(t, "SELECT 1\n", string(second.Pass.Out), "the whole line, shaped")
	require.Equal(t, "se", string(second.Pass.State), "and the start of the next one is still held")
}

func TestTheFinalChunkFlushesWhatIsHeld(t *testing.T) {
	sh := shaping(t)

	out, err := sh.Shape(t.Context(), chunk([]byte("quit"), invoke.Inbound, "", true))
	require.NoError(t, err)
	require.Equal(t, "QUIT", string(out.Pass.Out))
	require.Empty(t, out.Pass.State, "and it is holding nothing afterwards")
}

func TestTheTwoDirectionsAreShapedSeparately(t *testing.T) {
	sh := shaping(t)

	in, err := sh.Shape(t.Context(), chunk(nil, invoke.Inbound, "ping\n", false))
	require.NoError(t, err)
	require.Equal(t, "PING\n", string(in.Pass.Out))

	out, err := sh.Shape(t.Context(), chunk(nil, invoke.Outbound, "pong", false))
	require.NoError(t, err)
	require.Equal(t, "<pong", string(out.Pass.Out), "the outbound half is shaped its own way")
	require.Empty(t, out.Pass.State)
}

func TestATransformerCanCloseTheConnection(t *testing.T) {
	sh := shaping(t)

	out, err := sh.Shape(t.Context(), chunk(nil, invoke.Inbound, "stop it\n", false))
	require.NoError(t, err)
	require.Nil(t, out.Pass)
	require.Equal(t, "it was asked to stop", out.Refused)
}

func TestATransformerIsCalledWithNothingAtAll(t *testing.T) {
	sh := shaping(t)

	out, err := sh.Shape(t.Context(), &invoke.Stream{Conn: pgConn(), Way: invoke.Inbound})
	require.NoError(t, err)
	require.NotNil(t, out.Pass)
	require.Empty(t, out.Pass.Out)
	require.Empty(t, out.Pass.State)
}

func TestOnlyAProxyTransformerShapes(t *testing.T) {
	instance := start(t, tier(t, handler(t)), invoke.Spec{
		Name: "not_a_sieve", Artifact: "bafyhandler",
	})

	as, ok := instance.(invoke.Shapes)
	require.True(t, ok, "the tier's instance carries the method")

	_, err := as.Shape(t.Context(), chunk(nil, invoke.Inbound, "anything", false))
	require.ErrorIs(t, err, wasm.ErrUnsupportedWorld)
	require.ErrorContains(t, err, "shapes nothing")
}

func TestAProxyTransformerDirectsNothing(t *testing.T) {
	instance := start(t, tier(t, sieve(t)), invoke.Spec{
		Name:     "edge_sieve",
		Artifact: "bafysieve",
		Settings: map[string]string{wasm.SettingImplements: wasm.WorldProxyTransformer},
	})

	as, ok := instance.(invoke.Directs)
	require.True(t, ok)

	_, err := as.Direct(t.Context(), pgConn(), nil)
	require.ErrorIs(t, err, wasm.ErrUnsupportedWorld)
	require.ErrorContains(t, err, "directs nothing")
}
