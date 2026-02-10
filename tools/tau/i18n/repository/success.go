package repositoryI18n

import "github.com/taubyte/tau/tools/tau/i18n/printer"

func Imported(name, cloud string) {
	printer.SuccessWithNameOnCloud("Imported %s: %s on cloud %s", "repository", name, cloud)
}
