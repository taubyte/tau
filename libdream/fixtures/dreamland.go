package fixtures

import (
	"github.com/taubyte/tau/libdream"
)

func init() {
	libdream.RegisterFixture("fakeProject", fakeProject)
	libdream.RegisterFixture("injectProject", injectProject)
	libdream.RegisterFixture("setBranch", setBranch)
}
