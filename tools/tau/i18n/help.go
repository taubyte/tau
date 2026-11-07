package i18n

import "github.com/taubyte/tau/tools/tau/i18n/printer"

type helper struct{}

func Help() helper {
	return helper{}
}

func (helper) TokenMayBeExpired(login string) {
	printer.Out.InfoPrintfln("Token may be expired refresh with ( $tau login --new %s )", login)
}

func (helper) BeSureToCloneProject() {
	printer.Out.InfoPrintln("Be sure to clone the project ( $tau clone project )")
}

