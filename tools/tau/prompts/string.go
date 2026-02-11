package prompts

import (
	"github.com/AlecAivazis/survey/v2"
	"github.com/taubyte/tau/tools/tau/i18n/printer"
	"github.com/urfave/cli/v2"
)

type stringPromptMethod func(c *cli.Context, prompt string, prev ...string) string

func RequiredString(c *cli.Context, prompt string, f stringPromptMethod, prev ...string) string {
	ret := f(c, prompt, prev...)
	for ret == "" {
		if UseDefaults {
			panic(RequiredInDefaultsModeError(prompt))
		}
		printer.Out.WarningPrintln(Required)
		ret = f(c, prompt, prev...)
	}
	return ret
}

func RequiredStringWithValidator(c *cli.Context, prompt string, f stringPromptMethod, validator validateStringMethod, prev ...string) (string, error) {
	validate := func(ret string) error {
		if validator != nil {
			err := validator(ret)
			if err != nil {
				printer.Out.Warning(err)
				return err
			}
		}
		return nil
	}
	ret := f(c, prompt, prev...)

	var err error
	if len(ret) > 0 {
		err = validate(ret)
	}

	for ret == "" || err != nil {
		if ret == "" && UseDefaults {
			return "", RequiredInDefaultsModeError(prompt)
		}
		if err == nil {
			printer.Out.WarningPrintln(Required)
		}
		ret = f(c, prompt, prev...)

		if len(ret) > 0 {
			err = validate(ret)
		}
	}
	return ret, nil
}

func GetOrAskForAStringValue(c *cli.Context, field string, label string, prev ...string) string {
	inp := &survey.Input{
		Message: label,
	}

	if len(prev) != 0 {
		inp.Default = prev[0]
	}

	val := c.String(field)
	if val == "" {
		if UseDefaults {
			if len(prev) > 0 {
				return prev[0]
			}
			return ""
		}
		AskOne(inp, &val)
	}

	// Unset the flag to prevent it from circling back into the prompt
	if len(field) > 0 && c.IsSet(field) {
		err := c.Set(field, "")
		if err != nil {
			panic(err)
		}
	}

	return val
}

type validateStringMethod func(string) error

type validateRequiredStringHelper struct {
	field  string
	prompt string
	prev   []string

	validator validateStringMethod
}

func validateAndRequireString(c *cli.Context, cnf validateRequiredStringHelper) (string, error) {
	return RequiredStringWithValidator(c, cnf.prompt, func(*cli.Context, string, ...string) (ret string) {
		return GetOrAskForAStringValue(c, cnf.field, cnf.prompt, cnf.prev...)
	}, cnf.validator)
}

type validateStringHelper struct {
	field     string
	prompt    string
	prev      []string
	validator func(string) error
}

func validateString(c *cli.Context, cnf validateStringHelper) (ret string) {
	for {
		ret = GetOrAskForAStringValue(c, cnf.field, cnf.prompt, cnf.prev...)

		if ret == "" && UseDefaults {
			return ""
		}
		err := cnf.validator(ret)
		if err != nil {
			printer.Out.Warning(err)
		} else {
			break
		}

		// Unset the flag to prevent it from circling back into the prompt
		if len(cnf.field) > 0 && c.IsSet(cnf.field) {
			err = c.Set(cnf.field, "")
			if err != nil {
				panic(err)
			}
		}
	}

	return
}

func GetOrRequireAString(c *cli.Context, field, prompt string, validator validateStringMethod, prev ...string) (string, error) {
	return validateAndRequireString(c, validateRequiredStringHelper{
		field:     field,
		prompt:    prompt,
		prev:      prev,
		validator: validator,
	})
}
