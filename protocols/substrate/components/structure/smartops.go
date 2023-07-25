package structure

import (
	"github.com/taubyte/go-interfaces/services/substrate"
)

var _ substrate.SmartOpsService = &TestSmartOps{}

type TestSmartOps struct {
	substrate.Service
}

func (ts *TestSmartOps) Run(caller substrate.SmartOpEventCaller, smartOpIds []string) (uint32, error) {
	return 0, nil
}
