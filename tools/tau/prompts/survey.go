package prompts

import (
	"os"

	"github.com/AlecAivazis/survey/v2"
	"github.com/AlecAivazis/survey/v2/terminal"
	"github.com/taubyte/tau/tools/tau/states"
)

func handleError(err error) {
	if err != nil {
		states.ContextC()
		if err != terminal.InterruptErr {
			panic(err)
		}
		os.Exit(1)
	}
}

func AskOne(p survey.Prompt, response interface{}, opts ...survey.AskOpt) {
	err := survey.AskOne(p, response, opts...)
	handleError(err)
}
func Ask(qs []*survey.Question, response interface{}, opts ...survey.AskOpt) {
	err := survey.Ask(qs, response, opts...)
	handleError(err)
}
