package libraryI18n

import "github.com/taubyte/tau/tools/tau/i18n/printer"

type helper struct{}

func Help() helper {
	return helper{}
}

func (helper) BeSureToCloneLibrary() {
	printer.Out.InfoPrintfln("Be sure to clone the library ( $tau clone library )")
}

func (helper) LibraryAlreadyCloned(dir string) {
	printer.Out.InfoPrintfln("Library already cloned: %s", dir)
}
