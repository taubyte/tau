package counters

import (
	"github.com/ipfs/go-log/v2"
	"github.com/taubyte/go-interfaces/services/substrate"
)

var subLogger = log.Logger("substrate.service.counter")

// TODO Counters Need to be redone
func New(srv substrate.Service) (service substrate.CounterService, err error) {
	return &unImplementedService{}, nil

	// service = &Service{
	// 	Service:    srv,
	// 	ledger:     make(map[string]counters.Metric),
	// 	metricChan: make(chan *counters.WrappedMetric, 1024*1024),
	// 	ledgerLock: sync.RWMutex{},
	// }

	// if service.billingClient, err = billing.New(srv.Node().Context(), srv.Node()); err != nil {
	// 	return nil, fmt.Errorf("failed creating billing client with %v", err)
	// }

	// service.Start()
}
