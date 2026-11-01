package schema_test

import (
	"context"
	"testing"

	"github.com/spf13/afero"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"gotest.tools/v3/assert"
)

// compileInEnv compiles one document for env: `segment_env` overrides `segment`.
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
	"runtime: wasm\n" +
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

func TestAnUnmarkedKeyCannotBeEnved(t *testing.T) {
	_, err := compileInEnv(t, "preview", "id: QmNf1SA5DVFbNTVYYPvKqTLLYzGqPZAmtLqYmYnFCEcvLh\n"+
		"id_preview: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Za\n"+
		"runtime: wasm\n"+
		"execution: {call: c, timeout: 1s, memory: 1MB}\n")
	assert.ErrorContains(t, err, "unknown key 'id_preview'")
}

func TestAnUndeclaredEnvironmentIsATypo(t *testing.T) {
	_, err := compileInEnv(t, "preview", "id: QmNf1SA5DVFbNTVYYPvKqTLLYzGqPZAmtLqYmYnFCEcvLh\n"+
		"runtime: wasm\n"+
		"execution:\n    call: c\n    timeout: 1s\n    timeout_prevew: 5s\n    memory: 1MB\n")
	assert.ErrorContains(t, err, "unknown key 'timeout_prevew'")
}

func TestAPlacementDiffersPerEnvironment(t *testing.T) {
	const doc = "id: QmNf1SA5DVFbNTVYYPvKqTLLYzGqPZAmtLqYmYnFCEcvLh\n" +
		"runtime: wasm\n" +
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

func TestAPolicyIsTheSameInEveryDeployment(t *testing.T) {
	fn, err := compileInEnv(t, "preview", "id: QmNf1SA5DVFbNTVYYPvKqTLLYzGqPZAmtLqYmYnFCEcvLh\n"+
		"runtime: wasm\n"+
		"execution: {call: c, timeout: 1s, memory: 1MB}\n"+
		"regions:\n    failover:\n        main: region-1\n        standby: region-2\n    pinned_preview: [region-2]\n")
	assert.NilError(t, err, "each key is legal on its own, so the compile cannot refuse it")
	assert.Equal(t, nested(fn, "regions", "policy"), "failover")
}
