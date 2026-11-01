package wasm

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
)

// The clamp is a security rule: a guest raising max-tokens would lift its author's
// cost ceiling.

func ceiling(n string) invoke.Grant {
	g := invoke.Grant{Kind: modelKind, Label: "brain", Ops: []string{opInfer}}
	if n != "" {
		g.Settings = map[string]string{SettingMaxTokens: n}
	}
	return g
}

func tokens(n uint32) *uint32 { return &n }

func TestAskingForMoreThanTheResourceAllowsGetsTheResources(t *testing.T) {
	req := &invoke.Inference{MaxTokens: tokens(100000)}
	clamp(req, ceiling("500"))

	require.NotNil(t, req.MaxTokens)
	require.Equal(t, uint32(500), *req.MaxTokens)
}

func TestAskingForLessIsHonoured(t *testing.T) {
	req := &invoke.Inference{MaxTokens: tokens(50)}
	clamp(req, ceiling("500"))

	require.Equal(t, uint32(50), *req.MaxTokens)
}

func TestSayingNothingGetsTheResourcesCeiling(t *testing.T) {
	req := &invoke.Inference{}
	clamp(req, ceiling("500"))

	require.NotNil(t, req.MaxTokens)
	require.Equal(t, uint32(500), *req.MaxTokens)
}

func TestNoDeclaredCeilingLeavesTheRequestAlone(t *testing.T) {
	req := &invoke.Inference{MaxTokens: tokens(100000)}
	clamp(req, ceiling(""))
	require.Equal(t, uint32(100000), *req.MaxTokens)

	empty := &invoke.Inference{}
	clamp(empty, ceiling(""))
	require.Nil(t, empty.MaxTokens, "and absent stays absent")
}

func TestAnUnreadableCeilingIsIgnored(t *testing.T) {
	for _, bad := range []string{"lots", "-1", "0", ""} {
		req := &invoke.Inference{MaxTokens: tokens(4096)}
		clamp(req, ceiling(bad))
		require.Equal(t, uint32(4096), *req.MaxTokens, "setting %q", bad)
	}
}
