package i18n

import "github.com/pterm/pterm"

type helper struct{}

func Help() helper {
	return helper{}
}

func (helper) HaveYouLoggedIn() {
	pterm.Info.Println("Have you logged in ( $tau login )")
}

func (helper) HaveYouSelectedANetwork() {
	pterm.Info.Println("Have you selected a network with ( $tau select network )")
}

func (helper) TokenMayBeExpired(login string) {
	pterm.Info.Printfln("Token may be expired refresh with ( $tau login --new %s )", login)
}

func (helper) BeSureToCloneProject() {
	pterm.Info.Println("Be sure to clone the project ( $tau clone project )")
}

func (helper) BeSureToSelectProject() {
	pterm.Info.Println("Have you selected a project? ( $tau select project )")
}
