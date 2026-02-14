package prompts

import (
	"os"

	"github.com/AlecAivazis/survey/v2"
	"github.com/AlecAivazis/survey/v2/terminal"
)

func handleError(err error) {
	if err != nil {
		if err != terminal.InterruptErr {
			panic(err)
		}
		os.Exit(1)
	}
}

func AskOne(p survey.Prompt, response any, opts ...survey.AskOpt) {
	err := survey.AskOne(p, response, opts...)
	handleError(err)
}
func Ask(qs []*survey.Question, response any, opts ...survey.AskOpt) {
	err := survey.Ask(qs, response, opts...)
	handleError(err)
}
