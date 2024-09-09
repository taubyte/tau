package projectI18n

import (
	"github.com/taubyte/tau/tools/tau/i18n/printer"
)

func success(prefix, name string) {
	printer.SuccessWithName("%s project: %s", prefix, name)
}

func successOnNetwork(prefix, name, network string) {
	printer.SuccessWithNameOnNetwork("%s project: %s on network: %s", prefix, name, network)
}

func DeselectedProject(name string) {
	success("Deselected", name)
}

func SelectedProject(name string) {
	success("Selected", name)
}

func CreatedProject(name string) {
	success("Created", name)
}

func PushedProject(name string) {
	success("Pushed", name)
}

func PulledProject(name string) {
	success("Pulled", name)
}

func CheckedOutProject(name, branch string) {
	printer.SuccessWithName("Checked out branch `%s` on project `%s`", branch, name)
}

func ImportedProject(name, networkFQDN string) {
	successOnNetwork("Imported", name, networkFQDN)
}

func RemovedProject(name, networkFQDN string) {
	successOnNetwork("Removed", name, networkFQDN)
}
