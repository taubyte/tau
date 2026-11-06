package emit_test

import (
	"context"
	"strings"
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

	// A website whose package builds with a step naming no toolchain image — the exact
	// shape a Vercel import produced before it started writing one.
	result := importer.Result{Source: "test"}
	site := map[string]any{}
	result.Runs(site, "site", map[string]any{
		"type": "assets", "output": "dist",
		"build": map[string]any{"steps": []any{map[string]any{"name": "build", "commands": []string{"npm run build"}}}},
	})
	result.Set("website", "site", site)

	report, err := emit.To(fs, "/project", "shop", result)
	require.NoError(t, err)

	require.False(t, report.Compiles, "it does not compile")
	require.Contains(t, report.Why, "image")

	var named []string
	for _, m := range report.Missing {
		named = append(named, m.Kind+"/"+m.Name+": "+m.Field)
	}
	require.Contains(t, strings.Join(named, "\n"), "image",
		"the to-do list must name what blocks the compile; it named %v", named)
}

func TestABranchThatWasNotSelectedRequiresNothing(t *testing.T) {
	fs := afero.NewMemMapFs()

	result := importer.Result{Source: "test"}
	// A stream on the swarm's own engine. Kafka's host and credentials are
	// required ONLY on the kafka branch, which this did not write.
	result.Set("stream", "orders", map[string]any{
		"distributed": map[string]any{},
	})

	report, err := emit.To(fs, "/project", "shop", result)
	require.NoError(t, err)

	for _, m := range report.Missing {
		require.NotContains(t, m.Field, "kafka", "a branch nobody wrote: %v", report.Missing)
		require.NotContains(t, m.Field, "nats", "a branch nobody wrote: %v", report.Missing)
	}
	require.True(t, report.Compiles, "%s", report.Why)
}
