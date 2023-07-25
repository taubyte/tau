package counters

import (
	"github.com/ipfs/go-log/v2"
	"github.com/taubyte/go-interfaces/services/substrate"
)

var subLogger = log.Logger("substrate.service.counter")

// TODO Counters Need to be redone
func New(srv substrate.Service) (service substrate.CounterService, err error) {
	return &unImplementedService{}, nil
}
