package dreamI18n

import "github.com/pterm/pterm"

type helper struct{}

func Help() helper {
	return helper{}
}

func (helper) IsAValidBinary() {
	pterm.Info.Printfln("command `dream` failed, do you have a valid binary? <insert docs link>")
}

func (helper) IsDreamlandRunning() {
	pterm.Info.Printfln("Have you started dreamland? ( $tau dream )")
}
