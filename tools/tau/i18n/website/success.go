package websiteI18n

import (
	"fmt"

	"github.com/taubyte/tau/tools/tau/i18n/printer"
)

func success(prefix, name string) {
	printer.SuccessWithName("%s website: %s", prefix, name)
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

func Registered(url string) {
	printer.SuccessWithName("%s repository: %s", "Registered", url)
}

// Do not implement, this is already verbose from git
// func Cloned(url string) {
// 	printer.SuccessWithName("%s repository: %s", "Cloned", url)
// }

func Pulled(url string) {
	printer.SuccessWithName("%s repository: %s", "Pulled", url)
}

func Pushed(url string, commitMessage string) {
	printer.SuccessWithName("%s repository: %s", fmt.Sprintf("Pushed: `%s` to", commitMessage), url)
}

func CheckedOut(url string, branch string) {
	printer.SuccessWithName("%s repository: %s", fmt.Sprintf("Checked out `%s` on", branch), url)
}
