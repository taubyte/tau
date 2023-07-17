package jobs

import (
	"github.com/taubyte/go-interfaces/services/monkey"
)

type mockMonkey struct {
	monkey.Service
}

type testContext struct {
	Context
}
