package prompts

import (
	"fmt"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/AlecAivazis/survey/v2/core"
	"github.com/pterm/pterm"
	"github.com/urfave/cli/v2"
)

func init() {
	survey.ErrorTemplate = pterm.Warning.Sprintln(`{{ .Error.Error }}`)
}

type MultiSelectConfig struct {
	Field    string
	Prompt   string
	Options  []string
	Previous []string
	Required bool
}

func MultiSelect(c *cli.Context, cnf MultiSelectConfig) (ret []string) {
	if len(cnf.Field) == 0 {
		panic(fmt.Sprintf(FieldNotDefinedInConfig, cnf))
	}

	if c.IsSet(cnf.Field) {
		ret = c.StringSlice(cnf.Field)
	} else {
		multiselectPrompt(&ret, cnf)
		return
	}

	formattedOptions := strings.ToLower(strings.Join(cnf.Options, ","))
	for _, selection := range ret {
		if !strings.Contains(formattedOptions, strings.ToLower(selection)) {
			pterm.Warning.Printfln(DoubleStringNotFound, cnf.Field, selection)
			multiselectPrompt(&ret, cnf)
			return
		}
	}

	if cnf.Required && len(ret) == 0 {
		multiselectPrompt(&ret, cnf)
		return
	}

	return
}

func multiselectPrompt(ret *[]string, cnf MultiSelectConfig) {
	panicIfPromptNotEnabledSelection(strings.Join(*ret, ", "), cnf.Prompt, cnf.Options)

	AskOne(&survey.MultiSelect{
		Message: cnf.Prompt,
		Options: cnf.Options,
		Default: cnf.Previous,
	}, ret, survey.WithValidator(func(ans interface{}) error {
		if cnf.Required && len(ans.([]core.OptionAnswer)) == 0 {
			return fmt.Errorf(StringIsRequired, cnf.Field)
		}

		return nil
	}))
}
