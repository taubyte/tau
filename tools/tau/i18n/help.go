package i18n

import "github.com/taubyte/tau/tools/tau/i18n/printer"

type helper struct{}

func Help() helper {
	return helper{}
}

func (helper) HaveYouLoggedIn() {
	printer.Out.InfoPrintln("Have you logged in ( $tau login )")
}

func (helper) HaveYouSelectedACloud() {
	printer.Out.InfoPrintln("Have you selected a cloud with ( $tau select cloud )")
}

func (helper) TokenMayBeExpired(login string) {
	printer.Out.InfoPrintfln("Token may be expired refresh with ( $tau login --new %s )", login)
}

func (helper) BeSureToCloneProject() {
	printer.Out.InfoPrintln("Be sure to clone the project ( $tau clone project )")
}

func (helper) BeSureToSelectProject() {
	printer.Out.InfoPrintln("Have you selected a project? ( $tau select project )")
}
