package wasm_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/invoke/wasm"
)

// workload_test drives `taubyte:workload/identity` — the one capability that is
// authority over nothing and therefore needs no grant.

// TestAWorkloadIsToldWhatItIs, every field of it, read back inside the guest —
// which is what shows a record with a list in the middle lowers correctly.
func TestAComponentLearnsWhatItIs(t *testing.T) {
	instance := start(t, tier(t, cacher(t), wasm.WithNode("12D3KooWNode")), invoke.Spec{
		Name:        "shop/prod/function/QmCheckout",
		Project:     "shop",
		Environment: "prod",
		Kind:        "function",
		Label:       "checkout",
		Scope:       []string{"shop", "eu"},
		Revision:    "bafyRevision",
		Artifact:    "bafyArtifact",
	})

	res := call(t, instance, "/whoami/x/", nil)
	require.Equal(t, 200, res.Status, string(res.Body))
	require.Equal(t,
		"shop/prod/function/checkout/[shop,eu]/bafyRevision/bafyArtifact/12D3KooWNode",
		string(res.Body))
}

// TestITNEEDSNoGrant, which is the whole argument for the capability existing
// at all: it answers about the instance and reaches nothing.
func TestAWorkloadWithNoAccessBlockStillKnowsItself(t *testing.T) {
	instance := start(t, tier(t, cacher(t)), invoke.Spec{
		Name: "fn", Project: "shop", Environment: "dev", Kind: "function", Label: "hello",
	})

	res := call(t, instance, "/whoami/x/", nil)
	require.Equal(t, 200, res.Status)
	require.Equal(t, "shop/dev/function/hello/[]//bafycomponent/", string(res.Body),
		"and an empty field is empty rather than invented")
}

// TestATIERWithNoNodeIdentityAnswersEmpty rather than naming something. A test
// runs that way, and so does anything embedding this package directly.
func TestAnUnnamedNodeIsEmptyAndNotGuessed(t *testing.T) {
	instance := start(t, tier(t, cacher(t)), invoke.Spec{Name: "fn", Project: "shop"})

	res := call(t, instance, "/whoami/x/", nil)
	require.Equal(t, "shop////[]//bafycomponent/", string(res.Body))
}
