package counters

import (
	"context"
	"sync"

	"github.com/taubyte/go-interfaces/services/billing"
	nodeIface "github.com/taubyte/go-interfaces/services/substrate"
	iface "github.com/taubyte/go-interfaces/services/substrate/counters"
)

var _ iface.Service = &Service{}

type Service struct {
	nodeIface.Service
	ledger     map[string]iface.Metric
	metricChan chan *iface.WrappedMetric
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
