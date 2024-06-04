package counters

import (
	"sync"

	"github.com/taubyte/tau/core/services/substrate"
	"github.com/taubyte/tau/core/services/substrate/counters"
)

type counter struct {
	substrate.Service
	ledger     map[string]counters.Metric
	metricChan chan *counters.WrappedMetric
	ledgerLock sync.RWMutex
}
