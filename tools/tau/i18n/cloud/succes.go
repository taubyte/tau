package cloudI18n

import "github.com/taubyte/tau/tools/tau/i18n/printer"

func Success(name string) {
	printer.Out.SuccessPrintfln("Connected to %s", printer.Out.SprintCyan(name))
}
