package mocks

import (
	"github.com/taubyte/go-interfaces/services/substrate"
)

func (mockedSmartOps) Run(caller substrate.SmartOpEventCaller, smartOpIds []string) (uint32, error) {
	return 0, nil
}

func (*mockedSmartOps) Close() error {
	return nil
}
