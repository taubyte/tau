package jobs

import (
	"github.com/taubyte/tau/core/services/monkey"
)

type mockMonkey struct {
	monkey.Service
}

type testContext struct {
	Context
}
