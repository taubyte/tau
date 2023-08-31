package counters

import (
	"sync"

	"github.com/taubyte/go-interfaces/services/substrate"
	"github.com/taubyte/go-interfaces/services/substrate/counters"
)

type counter struct {
	substrate.Service
	ledger     map[string]counters.Metric
	metricChan chan *counters.WrappedMetric
	ledgerLock sync.RWMutex
}
