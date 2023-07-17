package counters

import (
	"fmt"
	"sync"

	billing "bitbucket.org/taubyte/billing/api/p2p"
	"github.com/taubyte/go-interfaces/services/substrate/counters"

	nodeIface "github.com/taubyte/go-interfaces/services/substrate"
)

func New(srv nodeIface.Service) (service *Service, err error) {
	service = &Service{
		Service:    srv,
		ledger:     make(map[string]counters.Metric),
		metricChan: make(chan *counters.WrappedMetric, 1024*1024),
		ledgerLock: sync.RWMutex{},
	}

	if service.billingClient, err = billing.New(srv.Node().Context(), srv.Node()); err != nil {
		return nil, fmt.Errorf("failed creating billing client with %v", err)
	}

	service.Start()
	return
}
