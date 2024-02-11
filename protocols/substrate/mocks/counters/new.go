package counters

import (
	"github.com/ipfs/go-log/v2"

	"github.com/taubyte/go-interfaces/services/substrate"
	"github.com/taubyte/go-interfaces/services/substrate/counters"
)

var logger = log.Logger("tau.counter.mocks.service")

func New(srv substrate.Service) substrate.CounterService {
	c := &counter{
		Service:    srv,
		ledger:     make(map[string]counters.Metric),
		metricChan: make(chan *counters.WrappedMetric),
	}

	c.Start()
	return c
}
