package counters

import (
	"context"
	"fmt"
	"time"

	"github.com/taubyte/go-interfaces/services/substrate"
)

func (s *Service) Push(wms ...*substrate.WrappedMetric) {
	for _, wm := range wms {
		s.metricChan <- wm
	}
}

func (s *Service) Start() {
	s.reportCtx, s.reportCtxC = context.WithCancel(s.Context())

	go func() {
		for {
			select {
			case <-s.reportCtx.Done():
				return
			case metric := <-s.metricChan:
				s.ledgerLock.RLock()
				genericMetric, ok := s.ledger[metric.Key]
				s.ledgerLock.RUnlock()
				if !ok {
					s.ledgerLock.Lock()
					s.ledger[metric.Key] = metric.Metric
					s.ledgerLock.Unlock()
				} else {
					if err := genericMetric.Aggregate(metric.Metric); err != nil {
						subLogger.Errorf("aggregate metric failed with: %s", err)
					}
				}
			case <-time.After(substrate.DefaultReportTime):
				temp := make(map[string]substrate.Metric)
				s.ledgerLock.Lock()
				ledger := s.ledger
				s.ledger = make(map[string]substrate.Metric)
				s.ledgerLock.Unlock()

				for key, metric := range ledger {
					temp[key] = metric
				}

				go s.report(temp)
			}
		}
	}()
}

func (s *Service) report(ledger map[string]substrate.Metric) {
	err := s.billingClient.Report(ledger)
	if err != nil {
		subLogger.Errorf(fmt.Sprintf("Failed reporting to billing with %v", err))
	}
}

func (s *Service) Context() context.Context {
	return s.Node().Context()
}
