package fixtures

import "github.com/taubyte/tau/dream"

func init() {
	dream.RegisterFixture("fakeProject", fakeProject)
	dream.RegisterFixture("injectProject", injectProject)
	dream.RegisterFixture("setBranch", setBranch)
}
