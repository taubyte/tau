package libraryI18n

import "github.com/pterm/pterm"

type helper struct{}

func Help() helper {
	return helper{}
}

func (helper) BeSureToCloneLibrary() {
	pterm.Info.Printfln("Be sure to clone the library ( $tau clone library )")
}

func (helper) LibraryAlreadyCloned(dir string) {
	pterm.Info.Printfln("Library already cloned: %s", dir)
}
