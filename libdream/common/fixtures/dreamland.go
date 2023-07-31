package fixtures

import (
	dreamlandRegistry "github.com/taubyte/tau/libdream/registry"
)

func init() {
	dreamlandRegistry.Fixture("fakeProject", fakeProject)
	dreamlandRegistry.Fixture("injectProject", injectProject)
	dreamlandRegistry.Fixture("setBranch", setBranch)
}
