package prompts

import (
	"fmt"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/pterm/pterm"
	promptsI18n "github.com/taubyte/tau/tools/tau/i18n/prompts"
	"github.com/urfave/cli/v2"
)

func SelectInterface(names []string, prompt, _default string) (selectedInterface string, err error) {
	if len(names) == 0 {
		err = fmt.Errorf(SelectPromptNoOptions, prompt)
		return
	}

	selector := &survey.Select{
		Message: prompt,
		Options: names,
	}

	if len(_default) == 0 {
		selector.Default = names[0]
	} else {
		selector.Default = _default
	}

	AskOne(selector, &selectedInterface)

	return
}

func SelectInterfaceField(ctx *cli.Context, options []string, field string, prompt string, prev ...string) (selected string, err error) {
	var _default string
	if len(prev) > 0 {
		_default = prev[0]
	}

	if ctx.IsSet(field) {
		method := strings.ToLower(ctx.String(field))
		for _, optMethod := range options {
			if method == strings.ToLower(optMethod) {
				return optMethod, nil
			}
		}

		pterm.Warning.Println(promptsI18n.InvalidType(method, options))
	}

	return SelectInterface(options, prompt, _default)
}
