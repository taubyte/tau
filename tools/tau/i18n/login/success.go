package loginI18n

import (
	"github.com/taubyte/tau/tools/tau/i18n/printer"
)

func success(prefix, name string) {
	printer.SuccessWithName("%s profile: %s", prefix, name)
}

func Created(name string) {
	success("Created", name)
}

func CreatedDefault(name string) {
	success("Created default", name)
}

func Selected(name string) {
	success("Selected", name)
}
