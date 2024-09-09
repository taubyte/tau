package prompts

import (
	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/validate"
	"github.com/urfave/cli/v2"
)

func GetOrRequireAPath(c *cli.Context, prompt string, prev ...string) string {
	return validateAndRequireString(c, validateRequiredStringHelper{
		field:     flags.Path.Name,
		prompt:    prompt,
		prev:      prev,
		validator: validate.VariablePathValidator,
	})
}
