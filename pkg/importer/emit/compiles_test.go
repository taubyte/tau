package emit_test

import (
	"context"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/importer"
	"github.com/taubyte/tau/pkg/importer/emit"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// compiles is the assertion every test here really makes: tcc reads what was
// written and produces an object.
//
// IT USES THE REAL COMPILER, not the session's own validate, because the two
// are different claims. Validate says the fields are individually acceptable;
// this says the project is a project.
func compiles(t *testing.T, fs afero.Fs, dir string) {
	t.Helper()

	c, err := schema.New(schema.WithVirtual(fs, dir), schema.WithBranch("main"))
	require.NoError(t, err)

	obj, _, err := c.Compile(context.Background())
	require.NoError(t, err)
	require.NotNil(t, obj)
}

func TestAHoleInTheOutputIsNamedInTheReport(t *testing.T) {
	fs := afero.NewMemMapFs()

	// A website built by commands, with no toolchain image — the exact shape a
	// Vercel import produced before it started writing one.
	result := importer.Result{Source: "test"}
	result.Set("website", "site", map[string]any{
		"build": map[string]any{
			"commands": map[string]any{"run": "npm run build", "output": "dist"},
		},
	})

	report, err := emit.To(fs, "/project", "shop", result)
	require.NoError(t, err)

	require.False(t, report.Compiles, "it does not compile")
	require.Contains(t, report.Why, "build/commands/image")

	var named []string
	for _, m := range report.Missing {
		named = append(named, m.Field)
	}
	require.Contains(t, named, "build/commands/image",
		"the to-do list must name what blocks the compile; it named %v", named)
}

func TestABranchThatWasNotSelectedRequiresNothing(t *testing.T) {
	fs := afero.NewMemMapFs()

	result := importer.Result{Source: "test"}
	// A stream on the swarm's own engine. Kafka's host and credentials are
	// required ONLY on the kafka branch, which this did not write.
	result.Set("stream", "orders", map[string]any{
		"distributed": map[string]any{"local": false},
	})

	report, err := emit.To(fs, "/project", "shop", result)
	require.NoError(t, err)

	for _, m := range report.Missing {
		require.NotContains(t, m.Field, "kafka", "a branch nobody wrote: %v", report.Missing)
		require.NotContains(t, m.Field, "nats", "a branch nobody wrote: %v", report.Missing)
	}
	require.True(t, report.Compiles, "%s", report.Why)
}
