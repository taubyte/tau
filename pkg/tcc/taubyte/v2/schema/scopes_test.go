package schema_test

import (
	"testing"

	"gotest.tools/v3/assert"

	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// TestANACCESSENTRYISNOTASCOPE — the "one key, two things" rule, on the walk
// that was not applying it.
//
// An entry filed under a kind inside a resource is either a resource the
// container HOLDS or something that resource declares for ITSELF, keyed by the
// label of what it reaches. Resources() has asked interp.HeldResource since it
// was written. Scopes() asked only whether the entry had a `name` — so a
// container-kind access entry carrying the optional `name` override was
// reported as a nested scope, with the access LABEL standing in for a resource
// id, and ScopeKinds() then mapped a path that does not exist to a kind.
//
// IT IS A DISAGREEMENT AND NOT ONLY A WRONG ANSWER. pkg/project reads the index
// and never sees such an entry, so the explorer and the runtime were answering
// differently about what one project contains.
func TestAnAccessEntryIsNotReportedAsAScope(t *testing.T) {
	const (
		workflowID = "QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zb"
		queueID    = "QmTiDwfLBYpwPg1qvCzpFKif9GAnNm9St7NbKYAoQmgcSu"
	)

	obj := map[string]any{
		"object": map[string]any{
			"id": "QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR", "name": "shop",
			"workflow": map[string]any{
				workflowID: map[string]any{
					"name": "checkout",
					// what the lowering produced: keyed by id, genuinely held.
					"sync": map[string]any{queueID: map[string]any{"name": "checkout_reserve", "type": "queue"}},
					// what the resource declares for itself: keyed by the
					// author's label, and naming a kind that IS a scope.
					"agent": map[string]any{"helper": map[string]any{
						"name": "support", "ops": []any{"invoke"},
					}},
				},
			},
		},
	}

	var scopes []string
	for _, s := range schema.Scopes(obj) {
		scopes = append(scopes, s.Kind+"/"+s.Name)
	}
	assert.DeepEqual(t, scopes, []string{"workflow/checkout"})

	kinds := schema.ScopeKinds(obj)
	assert.Equal(t, kinds["checkout"], "workflow")
	_, invented := kinds["checkout/support"]
	assert.Assert(t, !invented,
		"an agent this workflow REACHES was reported as an agent it CONTAINS: %v", kinds)
}
