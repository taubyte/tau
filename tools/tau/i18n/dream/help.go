package dreamI18n

import "github.com/taubyte/tau/tools/tau/i18n/printer"

type helper struct{}

func Help() helper {
	return helper{}
}

func (helper) IsAValidBinary() {
	printer.Out.InfoPrintfln("command `dream` failed, do you have a valid binary? <insert docs link>")
}

func (helper) IsDreamRunning() {
	printer.Out.InfoPrintfln("Have you started dream? ( $tau dream )")
}
