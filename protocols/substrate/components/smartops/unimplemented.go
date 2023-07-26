package smartOps

import (
	"github.com/taubyte/go-interfaces/services/substrate"
	"github.com/taubyte/go-interfaces/services/substrate/smartops"
)

func New(srv substrate.Service) (substrate.SmartOpsService, error) {
	return &unImplementedService{srv}, nil
}

var _ substrate.SmartOpsService = &unImplementedService{}

type unImplementedService struct {
	substrate.Service
}

func (u *unImplementedService) Run(smartops.EventCaller, []string) (uint32, error) {
	return 0, nil
}

func (u *unImplementedService) Close() error {
	return nil
}
