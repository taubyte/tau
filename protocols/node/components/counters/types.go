package counters

import (
	"context"
	"sync"

	"github.com/taubyte/go-interfaces/services/billing"
	"github.com/taubyte/go-interfaces/services/substrate"
)

var _ substrate.CounterService = &Service{}

type unImplementedService struct {
	substrate.Service
	ctx context.Context
}

func (*unImplementedService) Close() error                         { return nil }
func (u *unImplementedService) Context() context.Context           { return u.ctx }
func (*unImplementedService) Push(wms ...*substrate.WrappedMetric) {}
func (*unImplementedService) Start()                               {}

type Service struct {
	substrate.Service
	ledger     map[string]substrate.Metric
	metricChan chan *substrate.WrappedMetric
	ledgerLock sync.RWMutex
	reportCtx  context.Context
	reportCtxC context.CancelFunc

	billingClient billing.Client
}

func (s *Service) Close() error {
	s.reportCtxC()
	s.billingClient.Close()

	s.ledgerLock.Lock()
	s.ledger = nil
	s.ledgerLock.Unlock()

	return nil
}
