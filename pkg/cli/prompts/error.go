package prompts

import "github.com/pterm/pterm"

func ValidateOk(err error) bool {
	if err != nil {
		panicIfPromptNotEnabledError(err)

		pterm.Warning.Println(err.Error())
		return false
	}
	return true
}
