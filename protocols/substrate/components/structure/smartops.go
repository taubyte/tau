package structure

import (
	"github.com/taubyte/go-interfaces/services/substrate"
	"github.com/taubyte/go-interfaces/services/substrate/smartops"
)

var _ substrate.SmartOpsService = &TestSmartOps{}

type TestSmartOps struct {
	substrate.Service
}

func (ts *TestSmartOps) Run(caller smartops.EventCaller, smartOpIds []string) (uint32, error) {
	return 0, nil
}
