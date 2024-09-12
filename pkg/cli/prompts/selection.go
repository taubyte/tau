package prompts

import (
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/pterm/pterm"
	"github.com/urfave/cli/v2"
)

func GetOrAskForSelection(c *cli.Context, field string, label string, items []string, prev ...string) string {
	val := c.String(field)
	for {
		if val == "" {
			panicIfPromptNotEnabledSelection(val, label, items)

			if len(prev) != 0 {
				AskOne(&survey.Select{
					Message: label,
					Options: items,
					Default: prev[0],
				}, &val)
				for _, i := range items {
					if val == i {
						return i
					}
				}
			} else {
				AskOne(&survey.Select{
					Message: label,
					Options: items,
				}, &val)
				for _, i := range items {
					if val == i {
						return i
					}
				}
			}
		}

		valLC := strings.ToLower(val)
		for _, i := range items {
			if valLC == strings.ToLower(i) {
				return i
			}
		}
		pterm.Warning.Printfln(StringIsNotAValidSelection, val, items)
		val = ""
	}
}
