package databaseI18n

import (
	"github.com/taubyte/tau/tools/tau/i18n/printer"
)

func success(prefix, name string) {
	printer.SuccessWithName("%s database: %s", prefix, name)
}

func Created(name string) {
	success("Created", name)
}

func Deleted(name string) {
	success("Deleted", name)
}

func Edited(name string) {
	success("Edited", name)
}
