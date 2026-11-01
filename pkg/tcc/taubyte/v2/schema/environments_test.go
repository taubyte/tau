package schema_test

import (
	"context"
	"sort"
	"testing"

	"github.com/spf13/afero"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"gotest.tools/v3/assert"
)

// The environments fixture is its own tree: three deployments, a shadow written inside
// one of them, and two documents that suffix keys for four names.
const envFixture = "../fixtures/environments"

// envsOfFixture reads the table off a filesystem and path, with no compiler.
func envsOfFixture(t *testing.T) []schema.Environment {
	t.Helper()
	envs, err := schema.EnvironmentsOf(afero.NewOsFs(), envFixture)
	assert.NilError(t, err)
	return envs
}

// byName, because the table's order varies with map iteration.
func byName(envs []schema.Environment) map[string]schema.Environment {
	out := make(map[string]schema.Environment, len(envs))
	for _, e := range envs {
		out[e.Name] = e
	}
	return out
}

func TestTheEnvironmentTableIsReadOffTheProject(t *testing.T) {
	envs := envsOfFixture(t)
	assert.Equal(t, len(envs), 3, "three deployments stand alone; the fourth is nested")

	at := byName(envs)

	production, ok := at["production"]
	assert.Assert(t, ok, "production missing from %v", envs)
	assert.DeepEqual(t, production.Branches, []string{"main", "master"})
	assert.DeepEqual(t, production.Regions, []string{"region-1", "region-2"})

	staging, ok := at["staging"]
	assert.Assert(t, ok, "staging missing from %v", envs)
	assert.DeepEqual(t, staging.Branches, []string{"release/*"})
	assert.DeepEqual(t, staging.Regions, []string{"region-3"})

	preview, ok := at["preview"]
	assert.Assert(t, ok, "preview missing from %v", envs)
	assert.DeepEqual(t, preview.Branches, []string{"*"})
	// absent means the cloud places it, which is the common case
	assert.Equal(t, len(preview.Regions), 0)

	// The nesting: only production carries one, and it is a whole Environment.
	assert.Equal(t, len(staging.Shadows), 0)
	assert.Equal(t, len(preview.Shadows), 0)
	assert.Equal(t, len(production.Shadows), 1)
	ab := production.Shadows[0]
	assert.Equal(t, ab.Name, "ab")
	assert.DeepEqual(t, ab.Branches, []string{"main"})
	// `main` feeds production AND its shadow. That is not a collision to resolve — it
	// is what a shadow is for: one commit, two deployments, one set of state.
	assert.DeepEqual(t, production.Branches[:1], ab.Branches)
}

func TestOfIsSetOnAShadowAndEmptyOnADeployment(t *testing.T) {
	envs := envsOfFixture(t)
	for _, e := range envs {
		assert.Equal(t, e.Of, "", "%q stands alone, so it shadows nothing", e.Name)
		for _, s := range e.Shadows {
			assert.Equal(t, s.Of, e.Name, "%q is written inside %q", s.Name, e.Name)
			// A shadow keeps no state of its own, so it has nowhere to put and
			// nothing to nest: the schema declares neither key under `shadows`.
			assert.Equal(t, len(s.Regions), 0, "a shadow places no state")
			assert.Equal(t, len(s.Shadows), 0, "a shadow carries no shadows")
		}
	}
}

func TestAProjectWithNoEnvironmentsIsRefused(t *testing.T) {
	for name, doc := range map[string]string{
		"absent": "id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nname: p\n",
		"empty":  "id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nname: p\nenvironments: {}\n",
	} {
		t.Run(name, func(t *testing.T) {
			fs := afero.NewMemMapFs()
			assert.NilError(t, afero.WriteFile(fs, "/project.yaml", []byte(doc), 0o644))
			c, err := schema.New(schema.WithVirtual(fs, "/"))
			assert.NilError(t, err)
			_, _, err = c.Compile(context.Background())
			assert.ErrorContains(t, err, "required group 'environments'")
		})
	}
}

func TestEnvironmentNamesIncludeShadows(t *testing.T) {
	got := schema.EnvironmentNames(envsOfFixture(t))
	sort.Strings(got)
	assert.DeepEqual(t, got, []string{"ab", "preview", "production", "staging"})

	c, err := schema.New(schema.WithLocal(envFixture), schema.WithBranch("main"),
		schema.WithEnvironments("production", "staging", "preview"))
	assert.NilError(t, err)
	_, _, err = c.Compile(context.Background())
	assert.ErrorContains(t, err, "unknown key 'timeout_ab'")
}

func TestWithProjectEnvironmentsMakesASuffixedKeyAField(t *testing.T) {
	undeclared, err := schema.New(schema.WithLocal(envFixture), schema.WithBranch("main"))
	assert.NilError(t, err)
	_, _, err = undeclared.Compile(context.Background())
	// which suffixed key it names first is map order; that it refuses one is the rule
	assert.ErrorContains(t, err, "unknown key")

	declared, err := schema.New(schema.WithLocal(envFixture), schema.WithBranch("main"),
		schema.WithProjectEnvironments(afero.NewOsFs(), envFixture))
	assert.NilError(t, err)
	obj, _, err := declared.Compile(context.Background())
	assert.NilError(t, err)

	// And the suffixed keys never reach the object: what compiled is a `timeout`.
	fn := namedResource(t, resources(t, obj.Flat(), "function"), "checkout")
	ex := fn["execution"].(map[string]any)
	for _, k := range []string{"timeout_preview", "timeout_ab", "memory_preview", "memory_staging"} {
		_, leaked := ex[k]
		assert.Assert(t, !leaked, "the suffixed key %q leaked into the object: %v", k, ex)
	}
}

func TestOneCompilerReadsEveryEnvironment(t *testing.T) {
	c, err := schema.New(schema.WithLocal(envFixture), schema.WithBranch("main"),
		schema.WithProjectEnvironments(afero.NewOsFs(), envFixture))
	assert.NilError(t, err)

	const (
		second = int64(1e9)
		mb     = int64(1e6)
	)
	for _, tc := range []struct {
		env                 string
		timeout, memory     int64
		cacheSize, cacheTTL int64
		whyItReadsThatWay   string
	}{
		// no environment at all, and the one that overrides nothing, read the same
		// document the same way — production is not a special case, it is the default.
		{"", 30 * second, 64 * mb, 128 * mb, 24 * 3600 * second, "the unsuffixed lines"},
		{"production", 30 * second, 64 * mb, 128 * mb, 24 * 3600 * second, "overrides nothing, so the base"},
		{"preview", 5 * second, 16 * mb, 8 * mb, 3600 * second, "overrides all four"},
		// a partial override takes the one key it names and leaves the rest alone,
		// including the whole other resource.
		{"staging", 30 * second, 32 * mb, 128 * mb, 24 * 3600 * second, "overrides memory only"},
		// THE SHADOW READS ITS OWN VALUE. It is a deployment like any other here: the
		// per-key override does not care that its state is production's.
		{"ab", 45 * second, 64 * mb, 128 * mb, 24 * 3600 * second, "overrides timeout only"},
	} {
		name := tc.env
		if name == "" {
			name = "no environment"
		}
		t.Run(name, func(t *testing.T) {
			obj, _, err := c.CompileFor(context.Background(), schema.InEnvironment(tc.env))
			assert.NilError(t, err)
			flat := obj.Flat()

			ex := namedResource(t, resources(t, flat, "function"), "checkout")["execution"].(map[string]any)
			assert.Equal(t, ex["timeout"], tc.timeout, tc.whyItReadsThatWay)
			assert.Equal(t, ex["memory"], tc.memory, tc.whyItReadsThatWay)

			sessions := namedResource(t, resources(t, flat, "cache"), "sessions")
			assert.Equal(t, sessions["size"], tc.cacheSize, tc.whyItReadsThatWay)
			assert.Equal(t, sessions["ttl"], tc.cacheTTL, tc.whyItReadsThatWay)
		})
	}
}
