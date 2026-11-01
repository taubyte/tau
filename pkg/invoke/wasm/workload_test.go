package wasm_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/invoke/wasm"
)

func TestAComponentLearnsWhatItIs(t *testing.T) {
	instance := start(t, tier(t, cacher(t), wasm.WithNode("12D3KooWNode")), invoke.Spec{
		Name:        "shop/prod/function/QmCheckout",
		Project:     "shop",
		Environment: "prod",
		Kind:        "function",
		Label:       "checkout",
		Scope:       []string{"shop", "local"},
		Revision:    "bafyRevision",
		Artifact:    "bafyArtifact",
	})

	res := call(t, instance, "/whoami/x/", nil)
	require.Equal(t, 200, res.Status, string(res.Body))
	require.Equal(t,
		"shop/prod/function/checkout/[shop,local]/bafyRevision/bafyArtifact/12D3KooWNode",
		string(res.Body))
}

func TestAWorkloadWithNoAccessBlockStillKnowsItself(t *testing.T) {
	instance := start(t, tier(t, cacher(t)), invoke.Spec{
		Name: "fn", Project: "shop", Environment: "dev", Kind: "function", Label: "hello",
	})

	res := call(t, instance, "/whoami/x/", nil)
	require.Equal(t, 200, res.Status)
	require.Equal(t, "shop/dev/function/hello/[]//bafycomponent/", string(res.Body),
		"and an empty field is empty rather than invented")
}

func TestAnUnnamedNodeIsEmptyAndNotGuessed(t *testing.T) {
	instance := start(t, tier(t, cacher(t)), invoke.Spec{Name: "fn", Project: "shop"})

	res := call(t, instance, "/whoami/x/", nil)
	require.Equal(t, "shop////[]//bafycomponent/", string(res.Body))
}
