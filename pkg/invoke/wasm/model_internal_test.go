package wasm

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
)

// model_internal_test is about the clamp, which is the one rule in this file
// that is a SECURITY rule rather than a translation: the vocabulary calls
// max_tokens "a cost ceiling as much as a length one", so a guest that could
// raise it would be lifting a limit its author wrote.

func ceiling(n string) invoke.Grant {
	g := invoke.Grant{Kind: modelKind, Label: "brain", Ops: []string{opInfer}}
	if n != "" {
		g.Settings = map[string]string{SettingMaxTokens: n}
	}
	return g
}

func tokens(n uint32) *uint32 { return &n }

// TestACALLERCANNOTRAISETHECEILING.
func TestAskingForMoreThanTheResourceAllowsGetsTheResources(t *testing.T) {
	req := &invoke.Inference{MaxTokens: tokens(100000)}
	clamp(req, ceiling("500"))

	require.NotNil(t, req.MaxTokens)
	require.Equal(t, uint32(500), *req.MaxTokens)
}

// TestACALLERCANASKFORLESS, which is why this is a clamp and not a refusal: a
// caller that wants a short answer should be able to say so.
func TestAskingForLessIsHonoured(t *testing.T) {
	req := &invoke.Inference{MaxTokens: tokens(50)}
	clamp(req, ceiling("500"))

	require.Equal(t, uint32(50), *req.MaxTokens)
}

// TestACALLERTHATASKSFORNOTHINGGETSTHECEILING, rather than the provider's own
// default — which would make the author's number apply only to callers that
// happened to name one.
func TestSayingNothingGetsTheResourcesCeiling(t *testing.T) {
	req := &invoke.Inference{}
	clamp(req, ceiling("500"))

	require.NotNil(t, req.MaxTokens)
	require.Equal(t, uint32(500), *req.MaxTokens)
}

// TestARESOURCEWITHNOCEILINGCLAMPSNOTHING, which is the vocabulary's own
// default: absent means the provider's rather than a number tau invented.
func TestNoDeclaredCeilingLeavesTheRequestAlone(t *testing.T) {
	req := &invoke.Inference{MaxTokens: tokens(100000)}
	clamp(req, ceiling(""))
	require.Equal(t, uint32(100000), *req.MaxTokens)

	empty := &invoke.Inference{}
	clamp(empty, ceiling(""))
	require.Nil(t, empty.MaxTokens, "and absent stays absent")
}

// TestAMALFORMEDCEILINGISNOCEILING rather than zero, which would make every
// answer empty on a resource whose setting got mangled.
func TestAnUnreadableCeilingIsIgnored(t *testing.T) {
	for _, bad := range []string{"lots", "-1", "0", ""} {
		req := &invoke.Inference{MaxTokens: tokens(4096)}
		clamp(req, ceiling(bad))
		require.Equal(t, uint32(4096), *req.MaxTokens, "setting %q", bad)
	}
}
