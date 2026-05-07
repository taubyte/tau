package fixtures

import "github.com/taubyte/tau/dream"

func init() {
	dream.RegisterFixture("fakeProject", fakeProject)
	dream.RegisterFixture("injectProject", injectProject)
	dream.RegisterFixture("setBranch", setBranch)
	dream.RegisterFixture("fakeAccount", fakeAccount)
	dream.RegisterFixture("injectAccount", injectAccount)
	dream.RegisterFixture("fakeMember", fakeMember)
	dream.RegisterFixture("injectMember", injectMember)
}
