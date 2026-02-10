package projectPrompts

import (
	"strings"
	"testing"

	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/prompts/internal/mock"
	"github.com/urfave/cli/v2"
	"gotest.tools/v3/assert"
)

// TestGetOrSelect_NameSet_NonInteractive verifies that when the name flag is set,
// we do not prompt (no "non-interactive" or "cannot prompt" error).
// GetOrSelect may still error from projectLib.ListResources (e.g. no config).
func TestGetOrSelect_NameSet_NonInteractive(t *testing.T) {
	ctx, err := mock.CLI{
		Flags: []cli.Flag{flags.Name},
		ToSet: map[string]string{flags.Name.Name: "myproject"},
	}.Run("prog", "--name", "myproject")
	assert.NilError(t, err)

	_, err = GetOrSelect(ctx, false)
	// We expect an error from ListResources or "no projects found", not from prompting
	if err != nil && strings.Contains(err.Error(), "cannot prompt") {
		t.Fatalf("should not prompt when name is set: %v", err)
	}
}
