package websiteI18n

import "github.com/pterm/pterm"

type helper struct{}

func Help() helper {
	return helper{}
}

func (helper) BeSureToCloneWebsite() {
	pterm.Info.Printfln("Be sure to clone the website ( $tau clone website )")
}

func (helper) WebsiteAlreadyCloned(dir string) {
	pterm.Info.Printfln("Website already cloned: %s", dir)
}
