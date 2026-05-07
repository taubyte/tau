package compiler

import (
	"context"
	"testing"

	"gotest.tools/v3/assert"
)

// TestCompile_CloudsBindings_NoCloud exercises the dream / `tau validate
// config` path: when the compiler isn't told which cloud it's targeting,
// the cloud-filter pass drops the entire `clouds` map without promoting
// any binding. The published object carries no `account` / `plan` keys.
//
// This is the right behaviour for non-cloud-aware tooling: the same
// project repo is valid in any cloud, and a stand-alone validation run
// shouldn't fabricate a binding.
func TestCompile_CloudsBindings_NoCloud(t *testing.T) {
	compiler, err := New(WithLocal("fixtures/clouds"), WithBranch("master"))
	assert.NilError(t, err)

	obj, _, err := compiler.Compile(context.Background())
	assert.NilError(t, err)

	flat := obj.Flat()
	objectAny, ok := flat["object"].(map[string]any)
	assert.Assert(t, ok, "object missing from Flat() output")

	_, hasClouds := objectAny["clouds"]
	assert.Assert(t, !hasClouds, "expected `clouds` map to be dropped, got: %v", objectAny["clouds"])

	_, hasAccount := objectAny["account"]
	_, hasPlan := objectAny["plan"]
	assert.Assert(t, !hasAccount, "no cloud → no `account` promotion")
	assert.Assert(t, !hasPlan, "no cloud → no `plan` promotion")
}

// TestCompile_CloudsBindings_PromotesActive exercises the production path:
// when the compiler is configured with `WithCloud("tau-cloud.io")`, the
// pass picks the matching entry from the project's `clouds:` map, sets
// `account` / `plan` on the project root, and drops the entire `clouds`
// map (including the staging entry that doesn't belong on this cloud).
//
// Runtime services downstream read `branches/.../projects/{id}/account` and
// `.../plan` directly — they don't see, or need to know, the FQDN they
// happen to be running on.
func TestCompile_CloudsBindings_PromotesActive(t *testing.T) {
	compiler, err := New(
		WithLocal("fixtures/clouds"),
		WithBranch("master"),
		WithCloud("tau-cloud.io"),
	)
	assert.NilError(t, err)

	obj, _, err := compiler.Compile(context.Background())
	assert.NilError(t, err)

	flat := obj.Flat()
	objectAny, ok := flat["object"].(map[string]any)
	assert.Assert(t, ok, "object missing from Flat() output")

	_, hasClouds := objectAny["clouds"]
	assert.Assert(t, !hasClouds, "expected the full `clouds` map to be dropped after promotion")

	assert.Equal(t, objectAny["account"], "acme", "active cloud's account should be promoted to root")
	assert.Equal(t, objectAny["plan"], "prod", "active cloud's plan should be promoted to root")
}

// TestCompile_CloudsBindings_PromotesStaging is the parity test for the
// other FQDN — same project repo, different cloud, different binding.
// Confirms the same compile producing different output per cloud.
func TestCompile_CloudsBindings_PromotesStaging(t *testing.T) {
	compiler, err := New(
		WithLocal("fixtures/clouds"),
		WithBranch("master"),
		WithCloud("staging.tau-cloud.io"),
	)
	assert.NilError(t, err)

	obj, _, err := compiler.Compile(context.Background())
	assert.NilError(t, err)

	flat := obj.Flat()
	objectAny, ok := flat["object"].(map[string]any)
	assert.Assert(t, ok, "object missing from Flat() output")

	_, hasClouds := objectAny["clouds"]
	assert.Assert(t, !hasClouds)

	assert.Equal(t, objectAny["account"], "acme")
	assert.Equal(t, objectAny["plan"], "staging-free")
}

// TestCompile_CloudsBindings_PartialFails — half-set entry (account but
// no plan) fails compile when the active cloud is the half-set one.
// Catching this inside TCC means `tau validate config` flags the bad
// shape too — not just the live monkey-side validator. Same project
// in a different cloud's compile is fine (the no-entry path).
func TestCompile_CloudsBindings_PartialFails(t *testing.T) {
	compiler, err := New(
		WithLocal("fixtures/clouds_partial"),
		WithBranch("master"),
		WithCloud("tau-cloud.io"),
	)
	assert.NilError(t, err)

	_, _, err = compiler.Compile(context.Background())
	assert.ErrorContains(t, err, "incomplete")
	assert.ErrorContains(t, err, "tau-cloud.io")
}

// TestCompile_CloudsBindings_PartialOnOtherCloud — same fixture, but the
// compiler is targeting a cloud the partial entry isn't for. The bad
// entry is dropped silently because the pass only inspects the matching
// FQDN. This is intentional: the half-set entry is a problem for that
// cloud's compile, not for unrelated clouds compiling the same repo.
func TestCompile_CloudsBindings_PartialOnOtherCloud(t *testing.T) {
	compiler, err := New(
		WithLocal("fixtures/clouds_partial"),
		WithBranch("master"),
		WithCloud("staging.tau-cloud.io"),
	)
	assert.NilError(t, err)

	obj, _, err := compiler.Compile(context.Background())
	assert.NilError(t, err)

	flat := obj.Flat()
	objectAny, ok := flat["object"].(map[string]any)
	assert.Assert(t, ok)
	_, hasClouds := objectAny["clouds"]
	assert.Assert(t, !hasClouds)
	_, hasAccount := objectAny["account"]
	_, hasPlan := objectAny["plan"]
	assert.Assert(t, !hasAccount)
	assert.Assert(t, !hasPlan)
}

// TestCompile_CloudsBindings_UnknownCloud — compiler points at a cloud the
// project doesn't pin. Drop without promotion; no error. The project is
// "valid for this cloud, just not bound to a plan here." The plan-presence
// gate is policy in `services/monkey/jobs/checkAccountPlan`, not in TCC.
func TestCompile_CloudsBindings_UnknownCloud(t *testing.T) {
	compiler, err := New(
		WithLocal("fixtures/clouds"),
		WithBranch("master"),
		WithCloud("offshore.tau-cloud.io"),
	)
	assert.NilError(t, err)

	obj, _, err := compiler.Compile(context.Background())
	assert.NilError(t, err)

	flat := obj.Flat()
	objectAny, ok := flat["object"].(map[string]any)
	assert.Assert(t, ok)

	_, hasClouds := objectAny["clouds"]
	assert.Assert(t, !hasClouds)
	_, hasAccount := objectAny["account"]
	_, hasPlan := objectAny["plan"]
	assert.Assert(t, !hasAccount)
	assert.Assert(t, !hasPlan)
}
