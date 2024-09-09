package repositoryI18n

import "github.com/pterm/pterm"

func TriggerBuild() {
	pterm.Info.Printfln("Trigger build for usage with: %s", pterm.FgGreen.Sprintf("$ tau push {resource-type}"))
}
