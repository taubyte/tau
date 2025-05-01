package printer

import "github.com/pterm/pterm"

func SuccessWithName(format, prefix, name string) {
	pterm.Success.Printfln(format, prefix, pterm.FgCyan.Sprint(name))
}

func SuccessWithNameOnNetwork(format, prefix, name, network string) {
	pterm.Success.Printfln(format, prefix, pterm.FgCyan.Sprint(name), pterm.FgCyan.Sprint(network))
}
