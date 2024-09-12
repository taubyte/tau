package prompts

import (
	"github.com/AlecAivazis/survey/v2"
	"github.com/urfave/cli/v2"
)

func GetOrAskForBool(c *cli.Context, field string, label string, prev ...bool) bool {
	if c.IsSet(field) {
		return c.Bool(field)
	}

	if !PromptEnabled {
		panicIfPromptNotEnabled(label)
	}

	_default := FalseSelect
	if len(prev) > 0 && prev[0] {
		_default = TrueSelect
	}

	var val survey.OptionAnswer
	AskOne(&survey.Select{
		Message: label,
		Options: []string{FalseSelect, TrueSelect},
		Default: _default,
	}, &val)

	return val.Value == TrueSelect
}

func GetOrAskForBoolDefaultTrue(c *cli.Context, field string, label string, prev ...bool) bool {
	if c.IsSet(field) {
		return c.Bool(field)
	}

	if !PromptEnabled {
		panicIfPromptNotEnabled(label)
	}

	_default := TrueSelect
	if len(prev) > 0 {
		if !prev[0] {
			_default = FalseSelect
		}
	}

	var val survey.OptionAnswer
	AskOne(&survey.Select{
		Message: label,
		Options: []string{FalseSelect, TrueSelect},
		Default: _default,
	}, &val)

	return val.Value == TrueSelect
}
