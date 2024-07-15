package prompts

import "github.com/AlecAivazis/survey/v2"

func ConfirmPrompt(label string) bool {
	confirm := false
	AskOne(&survey.Confirm{
		Message: label,
	}, &confirm)

	return confirm
}
