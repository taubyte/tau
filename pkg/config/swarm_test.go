package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

// A real node without a swarm key would join an open network; only dev mode
// (Dream, tests) may run without one.
func TestWithSourceNeedsSwarmKeyOutsideDevMode(t *testing.T) {
	src := &Source{
		Privatekey: base64.StdEncoding.EncodeToString([]byte("node-key")),
		Services:   []string{"seer"},
	}

	_, err := New(WithSource(src, SourceOptions{Root: t.TempDir(), DevMode: false}))
	if err == nil || !strings.Contains(err.Error(), "swarm key cannot be empty") {
		t.Fatalf("non-dev node without swarm key: got %v, want a refusal", err)
	}

	_, err = New(WithSource(src, SourceOptions{Root: t.TempDir(), DevMode: true}))
	if err != nil && strings.Contains(err.Error(), "swarm key") {
		t.Fatalf("dev mode must not require a swarm key: %v", err)
	}
}
