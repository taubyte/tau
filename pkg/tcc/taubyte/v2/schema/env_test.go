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
// CEILING and reads exactly like `size` does — `[region-1, region-2]` at a floor of two is four
// nodes, and a throwaway environment should not be billed for production's residency.
func TestAPlacementDiffersPerEnvironment(t *testing.T) {
	const doc = "id: QmNf1SA5DVFbNTVYYPvKqTLLYzGqPZAmtLqYmYnFCEcvLh\n" +
		"execution:\n    call: c\n    timeout: 1s\n    memory: 1MB\n" +
		"regions:\n    pinned: [region-1]\n    pinned_preview: [region-2]\n"

	for _, tc := range []struct {
		env  string
		want []string
	}{
		{"production", []string{"region-1"}}, // declared nowhere in the document: the base
		{"preview", []string{"region-2"}},
	} {
		t.Run(tc.env, func(t *testing.T) {
			fn, err := compileInEnv(t, tc.env, doc)
			assert.NilError(t, err)
			assert.DeepEqual(t, nested(fn, "regions", "in"), tc.want)

			// AND THE UNSUFFIXED BRANCH STILL NAMES THE CHOICE, which is the
			// PerEnv contract: the override sits BESIDE the key and replaces its
			// value, never its declaration.
			assert.Equal(t, nested(fn, "regions", "policy"), "pinned")
		})
	}
}

// TestAPolicyIsTheSameInEveryDeployment.
//
// The branch takes a suffix, which makes the incoherent thing SAYABLE: `failover:`
// with a `pinned_preview:` beside it resolves to one branch's policy and another
// branch's regions. It is a different resource in each deployment, which is the
// rule PerEnv states one segment up — write the whole resource for that
// environment instead. The schema cannot see it (both keys are legal on their
// own), so the audit does.
func TestAPolicyIsTheSameInEveryDeployment(t *testing.T) {
	fn, err := compileInEnv(t, "preview", "id: QmNf1SA5DVFbNTVYYPvKqTLLYzGqPZAmtLqYmYnFCEcvLh\n"+
		"execution: {call: c, timeout: 1s, memory: 1MB}\n"+
		"regions:\n    failover:\n        main: region-1\n        standby: region-2\n    pinned_preview: [region-2]\n")
	assert.NilError(t, err, "each key is legal on its own, so the compile cannot refuse it")
	assert.Equal(t, nested(fn, "regions", "policy"), "failover")
}
