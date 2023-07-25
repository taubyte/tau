package counters

import (
	"context"

	"github.com/taubyte/go-interfaces/services/substrate"
	"github.com/taubyte/go-interfaces/services/substrate/counters"
)

func New(srv substrate.Service) (service substrate.CounterService, err error) {
	return &unImplementedService{Service: srv}, nil
}

var _ substrate.CounterService = &unImplementedService{}

type unImplementedService struct {
	substrate.Service
}

func (*unImplementedService) Close() error                    { return nil }
func (u *unImplementedService) Context() context.Context      { return u.Service.Context() }
func (*unImplementedService) Push(...*counters.WrappedMetric) {}
func (*unImplementedService) Start()                          {}
func (*unImplementedService) Implemented() bool               { return false }
