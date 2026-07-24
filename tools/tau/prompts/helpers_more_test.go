package prompts_test

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/taubyte/tau/tools/tau/prompts/internal/mock"
	"github.com/urfave/cli/v2"
	"gotest.tools/v3/assert"
)

// The flag-backed prompt helpers read their value from a set flag (or fall back
// to the previous/default) without touching the terminal. Driven in defaults
// mode with the mock CLI, so no interactive I/O.
func withDefaults(t *testing.T) func() {
	t.Helper()
	prompts.UseDefaults = true
	return func() { prompts.UseDefaults = false }
}

func boolCtx(t *testing.T, name string, set bool) *cli.Context {
	t.Helper()
	toSet := map[string]string{}
	if set {
		toSet[name] = "true"
	}
	ctx, err := mock.CLI{Flags: []cli.Flag{&cli.BoolFlag{Name: name}}, ToSet: toSet}.Run()
	assert.NilError(t, err)
	return ctx
}

func TestBoolFlagHelpers(t *testing.T) {
	defer withDefaults(t)()

	assert.Equal(t, prompts.GetClone(boolCtx(t, flags.Clone.Name, true)), true)
	assert.Equal(t, prompts.GetPrivate(boolCtx(t, flags.Private.Name, true)), true)
	assert.Equal(t, prompts.GetGenerateRepository(boolCtx(t, flags.GenerateRepo.Name, true)), true)
	assert.Equal(t, prompts.GetUseACodeTemplate(boolCtx(t, flags.UseCodeTemplate.Name, true)), true)
	assert.Equal(t, prompts.GetOrAskForEmbedToken(boolCtx(t, flags.EmbedToken.Name, true)), true)

	// default-true helper: unset in defaults mode keeps the previous
	ctx := boolCtx(t, "flag", false)
	assert.Equal(t, prompts.GetOrAskForBoolDefaultTrue(ctx, "flag", "L", true), true)
}

func TestStringFlagHelpers(t *testing.T) {
	defer withDefaults(t)()

	ctx, err := mock.CLI{
		Flags: []cli.Flag{flags.Description, flags.RepositoryName},
		ToSet: map[string]string{flags.Description.Name: "desc", flags.RepositoryName.Name: "myrepo"},
	}.Run()
	assert.NilError(t, err)

	assert.Equal(t, prompts.GetOrAskForADescription(ctx), "desc")
	name, err := prompts.GetOrRequireARepositoryName(ctx)
	assert.NilError(t, err)
	assert.Equal(t, name, "myrepo")
}

func TestRenderTable(t *testing.T) {
	// pure formatting: exercises the truncation path with a long value
	prompts.RenderTable([][]string{
		{"Key", "value"},
		{"Long", string(make([]byte, 200))},
		{"short"}, // skipped (needs 2 columns)
	})
	prompts.RenderTableWithMerge([][]string{{"A", "1"}, {"A", "1"}})
}
