package repositoryI18n

import "github.com/taubyte/tau/tools/tau/i18n/printer"

func TriggerBuild() {
	printer.Out.InfoPrintfln("Trigger build for usage with: %s", printer.Out.SprintfGreen("$ tau push {resource-type}"))
}
