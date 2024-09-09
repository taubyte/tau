package prompts

import (
	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/validate"
	"github.com/urfave/cli/v2"
)

func GetOrRequireAMatch(c *cli.Context, prompt string, prev ...string) string {
	return validateAndRequireString(c, validateRequiredStringHelper{
		field:     flags.Match.Name,
		prompt:    prompt,
		prev:      prev,
		validator: validate.VariableMatchValidator,
	})
}

func GetMatchRegex(ctx *cli.Context, prev ...bool) bool {
	return GetOrAskForBool(ctx, flags.MatchRegex.Name, RegexPrompt, prev...)
}
