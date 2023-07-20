package mocks

import (
	"github.com/taubyte/go-interfaces/services/substrate"
)

type MockedSmartOps interface {
	substrate.SmartOpsService
}

type mockedSmartOps struct {
	substrate.Service
}
