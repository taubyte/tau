package prompts

import "github.com/taubyte/tau/tools/tau/i18n/printer"

func ValidateOk(err error) bool {
	if err != nil {
		panicIfPromptNotEnabledError(err)

		printer.Out.Warning(err)
		return false
	}
	return true
}
