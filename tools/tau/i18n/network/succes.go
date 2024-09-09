package networkI18n

import (
	"github.com/pterm/pterm"
)

func Success(name string) {
	pterm.Success.Printfln("Connected to %s", pterm.FgCyan.Sprintf(name))

}
