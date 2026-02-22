package build_test

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/cli/commands/build"
	"gotest.tools/v3/assert"
)

func TestCommand_Exists(t *testing.T) {
	assert.Assert(t, build.Command != nil)
	assert.Equal(t, build.Command.Name, "build")
	assert.Assert(t, len(build.Command.Subcommands) > 0)
}

func TestCommand_Subcommands(t *testing.T) {
	names := make(map[string]bool)
	for _, c := range build.Command.Subcommands {
		names[c.Name] = true
		switch c.Name {
		case "function", "website", "library":
			// each must have output flag
			var hasOutput bool
			for _, f := range c.Flags {
				if f.Names()[0] == "output" || f.Names()[0] == "o" {
					hasOutput = true
					break
				}
			}
			assert.Assert(t, hasOutput, "subcommand %s must have -o/--output flag", c.Name)
		}
	}
	assert.Assert(t, names["function"], "function subcommand must exist")
	assert.Assert(t, names["website"], "website subcommand must exist")
	assert.Assert(t, names["library"], "library subcommand must exist")
}
