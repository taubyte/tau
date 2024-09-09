package prompts

import (
	"github.com/AlecAivazis/survey/v2"
	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/urfave/cli/v2"
)

func ConfirmData(c *cli.Context, label string, data [][]string) bool {
	confirm := c.Bool(flags.Yes.Name)
	if !confirm {
		RenderTable(data)
		AskOne(&survey.Confirm{
			Message: label,
		}, &confirm)
	}

	return confirm
}

func ConfirmDataWithMerge(c *cli.Context, label string, data [][]string) bool {
	confirm := c.Bool(flags.Yes.Name)
	if !confirm {
		RenderTableWithMerge(data)
		AskOne(&survey.Confirm{
			Message: label,
		}, &confirm)
	}

	return confirm
}
