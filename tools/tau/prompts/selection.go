package prompts

import (
	"strings"

	"github.com/AlecAivazis/survey/v2"
	cliPrompts "github.com/taubyte/tau/pkg/cli/prompts"
	"github.com/taubyte/tau/tools/tau/i18n/printer"
	"github.com/urfave/cli/v2"
)

// defaultInOptions returns the option to use as default. Matches case-insensitively
// so old YAML configs with capitalized values (e.g. "Remote") still map to current
// options (e.g. "remote"). Returns "" if no match so survey doesn't get an invalid Default.
func defaultInOptions(items []string, prev []string) string {
	if len(prev) == 0 {
		return ""
	}
	wantLC := strings.ToLower(prev[0])
	for _, o := range items {
		if strings.ToLower(o) == wantLC {
			return o
		}
	}
	return ""
}

func GetOrAskForSelection(c *cli.Context, field string, label string, items []string, prev ...string) (string, error) {
	val := c.String(field)
	for {
		if val == "" {
			if UseDefaults {
				defaultOpt := defaultInOptions(items, prev)
				if defaultOpt != "" {
					return defaultOpt, nil
				}
				if len(items) > 0 {
					return items[0], nil
				}
				return "", RequiredInDefaultsModeError(label)
			}
			if cliPrompts.IsNonInteractive() {
				return "", cliPrompts.ErrNonInteractive
			}
			defaultOpt := defaultInOptions(items, prev)
			if defaultOpt != "" {
				AskOne(&survey.Select{
					Message: label,
					Options: items,
					Default: defaultOpt,
				}, &val)
				for _, i := range items {
					if val == i {
						return i, nil
					}
				}
			} else {
				AskOne(&survey.Select{
					Message: label,
					Options: items,
				}, &val)
				for _, i := range items {
					if val == i {
						return i, nil
					}
				}
			}
		}

		valLC := strings.ToLower(val)
		for _, i := range items {
			if valLC == strings.ToLower(i) {
				return i, nil
			}
		}
		printer.Out.WarningPrintfln(StringIsNotAValidSelection, val, items)
		val = ""
	}
}
