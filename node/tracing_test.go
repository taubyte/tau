package node

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace/noop"
)

// otelDefault is read before any test builds a node.
var otelDefault = otel.GetTracerProvider()

func TestNewInstallsTracerProvider(t *testing.T) {
	require.True(t, isOtelDefault(otelDefault), "otel's default provider is the one a node replaces")

	_, err := New(Config{})
	require.ErrorIs(t, err, ErrNoHost)
	require.False(t, isOtelDefault(otel.GetTracerProvider()),
		"after a node is built, a block operation's span does not go through otel's one mutex")
}

type ownTracerProvider struct{ noop.TracerProvider }

func TestNewKeepsInstalledTracerProvider(t *testing.T) {
	previous := otel.GetTracerProvider()
	defer otel.SetTracerProvider(previous)

	own := &ownTracerProvider{}
	otel.SetTracerProvider(own)
	_, err := New(Config{})
	require.ErrorIs(t, err, ErrNoHost)
	require.Same(t, own, otel.GetTracerProvider(), "a provider the process installed is the one it keeps")
}
