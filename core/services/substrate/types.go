package substrate

import (
	"context"
	"io"

	services "github.com/taubyte/tau/core/services"
	"github.com/taubyte/tau/core/services/substrate/counters"
	"github.com/taubyte/tau/core/services/substrate/smartops"
	"github.com/taubyte/tau/core/services/tns"
	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/p2p/streams/client"
)

type Service interface {
	services.HttpService
	// Vm Returns the  VM service attached to the Substrate
	Vm() vm.Service
	// Tns returns the Tns client attached to the Substrate
	Tns() tns.Client
	// Counter returns the counter service attached to the Substrate s
	Counter() CounterService
	// SmartOps returns the smartops service attached to the Substrate
	SmartOps() SmartOpsService
	Orbitals() []vm.Plugin

	Dev() bool
	Verbose() bool
	Context() context.Context
}

type ProxyClient interface {
	ProxyHTTP(host string, path string, method string, ops ...client.Option[client.Request]) (<-chan *client.Response, error)
	io.Closer
}

type SmartOpsService interface {
	Service
	Run(caller smartops.EventCaller, smartOpIds []string) (uint32, error)
}

type CounterService interface {
	Service
	Push(...*counters.WrappedMetric)
	Implemented() bool
}
