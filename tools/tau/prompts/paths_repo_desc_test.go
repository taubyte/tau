package prompts_test

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/taubyte/tau/tools/tau/prompts/internal/mock"
	"github.com/urfave/cli/v2"
	"gotest.tools/v3/assert"
)

func TestRequiredPaths_FromFlagValid(t *testing.T) {
	prompts.UseDefaults = true
	defer func() { prompts.UseDefaults = false }()

	ctx, err := mock.CLI{
		Flags: []cli.Flag{flags.Paths},
		ToSet: map[string]string{flags.Paths.Name: "/valid/path"},
	}.Run()
	assert.NilError(t, err)

	got := prompts.RequiredPaths(ctx)
	assert.DeepEqual(t, got, []string{"/valid/path"})
}

func TestGetGenerateRepository_FromFlag(t *testing.T) {
	prompts.UseDefaults = true
	defer func() { prompts.UseDefaults = false }()

	ctx, err := mock.CLI{
		Flags: []cli.Flag{flags.GenerateRepo},
	}.Run("--generate-repository")
	assert.NilError(t, err)

	got := prompts.GetGenerateRepository(ctx)
	assert.Assert(t, got)
}

func TestGetOrRequireARepositoryName_FromFlag(t *testing.T) {
	prompts.UseDefaults = true
	defer func() { prompts.UseDefaults = false }()

	ctx, err := mock.CLI{
		Flags: []cli.Flag{flags.RepositoryName},
		ToSet: map[string]string{flags.RepositoryName.Name: "my_repo"},
	}.Run()
	assert.NilError(t, err)

	got, err := prompts.GetOrRequireARepositoryName(ctx)
	assert.NilError(t, err)
	assert.Equal(t, got, "my_repo")
}

func TestGetOrAskForADescription_FromFlag(t *testing.T) {
	prompts.UseDefaults = true
	defer func() { prompts.UseDefaults = false }()

	ctx, err := mock.CLI{
		Flags: []cli.Flag{flags.Description},
		ToSet: map[string]string{flags.Description.Name: "A description"},
	}.Run()
	assert.NilError(t, err)

	got := prompts.GetOrAskForADescription(ctx)
	assert.Equal(t, got, "A description")
}
