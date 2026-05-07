package project_test

import (
	"testing"

	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"github.com/taubyte/tau/pkg/schema/project"
	"gotest.tools/v3/assert"
)

func TestSetBasic(t *testing.T) {
	p, err := internal.NewProjectEmpty()
	assert.NilError(t, err)

	p.Set(true,
		project.Id("testID"),
		project.Description("a different project"),
		project.Email("test@taubyte.com"),
	)

	eql(t, [][]any{
		{p.Get().Id(), "testID"},
		{p.Get().Description(), "a different project"},
		{p.Get().Email(), "test@taubyte.com"},
	})
}

// TestSetCloudBinding exercises the nested `clouds.<fqdn>.{account, plan}`
// fields. New projects emit these via `tau project new`; the config compiler
// (TCC) and the code compiler (monkey) both consult them by current cloud FQDN
// and call accounts.ResolvePlan.
func TestSetCloudBinding(t *testing.T) {
	p, err := internal.NewProjectEmpty()
	assert.NilError(t, err)

	p.Set(true, project.CloudBindingOp("tau-cloud.io", "acme", "prod"))

	binding, ok := p.Get().CloudBinding("tau-cloud.io")
	if !ok {
		t.Fatalf("expected binding for tau-cloud.io")
	}
	if binding.Account != "acme" || binding.Plan != "prod" {
		t.Fatalf("got binding %+v", binding)
	}
}

// TestSetMultipleCloudBindings — the same project repo may declare bindings
// for multiple clouds (e.g. prod + staging) so it can deploy to both with
// different plan tiers.
func TestSetMultipleCloudBindings(t *testing.T) {
	p, err := internal.NewProjectEmpty()
	assert.NilError(t, err)

	p.Set(true,
		project.CloudBindingOp("tau-cloud.io", "acme", "prod"),
		project.CloudBindingOp("staging.tau-cloud.io", "acme", "staging-free"),
	)

	clouds := p.Get().Clouds()
	if len(clouds) != 2 {
		t.Fatalf("expected 2 clouds, got %v", clouds)
	}

	prod, ok := p.Get().CloudBinding("tau-cloud.io")
	if !ok || prod.Plan != "prod" {
		t.Fatalf("prod binding wrong: %+v ok=%v", prod, ok)
	}
	staging, ok := p.Get().CloudBinding("staging.tau-cloud.io")
	if !ok || staging.Plan != "staging-free" {
		t.Fatalf("staging binding wrong: %+v ok=%v", staging, ok)
	}
}

// TestEmptyCloudBinding verifies that legacy projects (no `clouds:` block)
// and projects without an entry for the queried FQDN return (zero, false).
// The compiler treats this as "skip plan validation" — required for dream /
// local development where there's no accounts service.
func TestEmptyCloudBinding(t *testing.T) {
	p, err := internal.NewProjectEmpty()
	assert.NilError(t, err)

	binding, ok := p.Get().CloudBinding("tau-cloud.io")
	if ok {
		t.Fatalf("expected no binding on a fresh project; got %+v", binding)
	}
	if !binding.IsEmpty() {
		t.Fatalf("zero binding should report IsEmpty=true; got %+v", binding)
	}
	if clouds := p.Get().Clouds(); len(clouds) != 0 {
		t.Fatalf("expected no clouds; got %v", clouds)
	}
}

// TestCloudBinding_EmptyFQDN — defensive: querying with an empty FQDN should
// return (zero, false) rather than panicking or returning a bogus match.
func TestCloudBinding_EmptyFQDN(t *testing.T) {
	p, err := internal.NewProjectEmpty()
	assert.NilError(t, err)
	p.Set(true, project.CloudBindingOp("tau-cloud.io", "acme", "prod"))

	if _, ok := p.Get().CloudBinding(""); ok {
		t.Fatalf("empty FQDN should return ok=false")
	}
}
