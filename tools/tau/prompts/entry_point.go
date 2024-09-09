package prompts

import (
	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/validate"
	"github.com/urfave/cli/v2"
)

func GetOrRequireAnEntryPoint(c *cli.Context, prev ...string) string {
	return validateAndRequireString(c, validateRequiredStringHelper{
		field:  flags.EntryPoint.Name,
		prompt: EntryPointPrompt,
		prev:   prev,

		// TODO better validator
		validator: validate.VariableDescriptionValidator,
	})
}
