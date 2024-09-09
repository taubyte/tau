package printer

import "github.com/pterm/pterm"

func SuccessWithName(format, prefix, name string) {
	pterm.Success.Printfln(format, prefix, pterm.FgCyan.Sprintf(name))
}

func SuccessWithNameOnNetwork(format, prefix, name, network string) {
	pterm.Success.Printfln(format, prefix, pterm.FgCyan.Sprintf(name), pterm.FgCyan.Sprintf(network))
}
