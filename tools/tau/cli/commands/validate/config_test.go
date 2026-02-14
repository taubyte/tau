package validate_test

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/cli/commands/validate"
	"github.com/taubyte/tau/tools/tau/testutil"
	"gotest.tools/v3/assert"
)

// TCC fixture is not a git repo, so we pass --branch to avoid projectLib.Repository().Open().CurrentBranch().
const fixtureBranch = "main"

func TestValidateConfig_CommandExists(t *testing.T) {
	assert.Assert(t, validate.Command != nil)
	assert.Equal(t, validate.Command.Name, "validate")
	assert.Assert(t, len(validate.Command.Subcommands) > 0)
	var foundConfig bool
	for _, c := range validate.Command.Subcommands {
		if c.Name == "config" {
			foundConfig = true
			break
		}
	}
	assert.Assert(t, foundConfig, "config subcommand must exist")
}

func TestValidateConfig_HappyPath(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	err := testutil.RunCommand(validate.Command, "tau", "validate", "config", "--branch", fixtureBranch)
	assert.NilError(t, err)
}

func TestValidateConfig_WithBranchFlag(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	err := testutil.RunCommand(validate.Command, "tau", "validate", "config", "-b", fixtureBranch)
	assert.NilError(t, err)
}
