package websiteI18n

import "github.com/taubyte/tau/tools/tau/i18n/printer"

type helper struct{}

func Help() helper {
	return helper{}
}

func (helper) BeSureToCloneWebsite() {
	printer.Out.InfoPrintfln("Be sure to clone the website ( $tau clone website )")
}

func (helper) WebsiteAlreadyCloned(dir string) {
	printer.Out.InfoPrintfln("Website already cloned: %s", dir)
}
