package prompts

import (
	"github.com/AlecAivazis/survey/v2"
	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/urfave/cli/v2"
)

func ConfirmPrompt(c *cli.Context, label string) bool {
	confirm := c.Bool(flags.Yes.Name)
	if !confirm {
		AskOne(&survey.Confirm{
			Message: label,
		}, &confirm)

	}

	return confirm
}
