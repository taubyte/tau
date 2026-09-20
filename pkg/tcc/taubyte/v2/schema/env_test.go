package schema_test

import (
	"context"
	"testing"

	"github.com/spf13/afero"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"gotest.tools/v3/assert"
)

// ONE RULE: a path segment may carry an environment suffix, and `segment_env`
// overrides `segment`. The same document serves every environment — nothing is
// duplicated, nothing is patched, and what production runs is the unsuffixed line
// sitting right there beside it.
func compileInEnv(t *testing.T, env, doc string) (map[string]any, error) {
	t.Helper()
	fs := afero.NewMemMapFs()
	assert.NilError(t, copyTree(fs, "../fixtures/basic", "/"))
	assert.NilError(t, afero.WriteFile(fs, "/function/checkout.yaml", []byte(doc), 0o644))
	// The DECLARED set is what makes a suffix a field rather than a typo, and it is
	// the same set whichever environment is being compiled.
	opts := []schema.Option{schema.WithVirtual(fs, "/"), schema.WithBranch("main"),
		schema.WithEnvironments("production", "preview")}
	if env != "" {
		opts = append(opts, schema.WithEnvironment(env))
	}
	c, err := schema.New(opts...)
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	if err != nil {
		return nil, err
	}
	return namedResource(t, resources(t, obj.Flat(), "function"), "checkout"), nil
}

const envDoc = "id: QmNf1SA5DVFbNTVYYPvKqTLLYzGqPZAmtLqYmYnFCEcvLh\n" +
	"execution:\n" +
	"    call: c\n" +
	"    timeout: 20s\n" +
	"    timeout_preview: 5s\n" +
	"    memory: 32MB\n" +
	"    memory_preview: 16MB\n"

func TestAnEnvironmentReadsItsOwnValue(t *testing.T) {
	for _, tc := range []struct {
		env     string
		timeout any
		memory  any
	}{
		{"", "20s", "32MB"},
		{"production", "20s", "32MB"}, // declared nowhere in the document: the base
		{"preview", "5s", "16MB"},
	} {
		name := tc.env
		if name == "" {
			name = "no environment"
		}
		t.Run(name, func(t *testing.T) {
			fn, err := compileInEnv(t, tc.env, envDoc)
			assert.NilError(t, err)
			ex := fn["execution"].(map[string]any)
			assert.Equal(t, ex["timeout"], int64(0)+ex["timeout"].(int64), "timeout is a duration")
			// durations compile to nanoseconds; compare against the parsed form
			want := map[string]int64{"20s": 20e9, "5s": 5e9}[tc.timeout.(string)]
			assert.Equal(t, ex["timeout"], want)
			wantMem := map[string]int64{"32MB": 32e6, "16MB": 16e6}[tc.memory.(string)]
			assert.Equal(t, ex["memory"], wantMem)
			// the suffixed keys never reach the compiled object
			_, leaked := ex["timeout_preview"]
			assert.Assert(t, !leaked, "the suffixed key leaked into the object: %v", ex)
		})
	}
}

// A key the vocabulary does NOT mark cannot be enved — `id` is identity, and two
// environments of one resource share it.
func TestAnUnmarkedKeyCannotBeEnved(t *testing.T) {
	_, err := compileInEnv(t, "preview", "id: QmNf1SA5DVFbNTVYYPvKqTLLYzGqPZAmtLqYmYnFCEcvLh\n"+
		"id_preview: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Za\n"+
		"execution: {call: c, timeout: 1s, memory: 1MB}\n")
	assert.ErrorContains(t, err, "unknown key 'id_preview'")
}

// A suffix naming an environment the project does NOT declare is a typo, and it says
// so with the schema's own list — the closed set is what separates the two.
func TestAnUndeclaredEnvironmentIsATypo(t *testing.T) {
	_, err := compileInEnv(t, "preview", "id: QmNf1SA5DVFbNTVYYPvKqTLLYzGqPZAmtLqYmYnFCEcvLh\n"+
		"execution:\n    call: c\n    timeout: 1s\n    timeout_prevew: 5s\n    memory: 1MB\n")
	assert.ErrorContains(t, err, "unknown key 'timeout_prevew'")
}

// TestAPlacementDiffersPerEnvironment. What a preview deployment costs to place is a
// CEILING and reads exactly like `size` does — `[eu, us]` at a floor of two is four
// nodes, and a throwaway environment should not be billed for production's residency.
func TestAPlacementDiffersPerEnvironment(t *testing.T) {
	const doc = "id: QmNf1SA5DVFbNTVYYPvKqTLLYzGqPZAmtLqYmYnFCEcvLh\n" +
		"execution:\n    call: c\n    timeout: 1s\n    memory: 1MB\n" +
		"region:\n    pinned:\n        name: eu\n        name_preview: us\n"

	for _, tc := range []struct{ env, want string }{
		{"production", "eu"}, // declared nowhere in the document: the base
		{"preview", "us"},
	} {
		t.Run(tc.env, func(t *testing.T) {
			fn, err := compileInEnv(t, tc.env, doc)
			assert.NilError(t, err)
			assert.Equal(t, nested(fn, "region", "name"), tc.want)
			assert.Equal(t, nested(fn, "region", "policy"), "pinned")
		})
	}
}

// TestAPlacementPolicyCannotBeEnvironmentSuffixed, and nobody refused it: the policy IS
// the branch key, and envPath returns nil for a path ending in an Either — a key the
// document chooses rather than one the schema names. A resource that is `failover` in
// production and `pinned` in preview is a different resource, which is the same rule
// PerEnv states one segment up: write the whole resource for that environment instead.
func TestAPlacementPolicyCannotBeEnvironmentSuffixed(t *testing.T) {
	_, err := compileInEnv(t, "preview", "id: QmNf1SA5DVFbNTVYYPvKqTLLYzGqPZAmtLqYmYnFCEcvLh\n"+
		"execution: {call: c, timeout: 1s, memory: 1MB}\n"+
		"region:\n    pinned:\n        name: eu\n    pinned_preview:\n        name: us\n")
	assert.ErrorContains(t, err, "unknown key 'pinned_preview'")
}
