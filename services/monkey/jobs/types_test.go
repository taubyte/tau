package jobs

import (
	"github.com/taubyte/tau/core/services/hoarder"
	"github.com/taubyte/tau/core/services/monkey"
)

type mockMonkey struct {
	monkey.Service
	hoarder hoarder.Client
}

type testContext struct {
	Context
}
