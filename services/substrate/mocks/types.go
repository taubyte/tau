package mocks

import (
	"context"

	httpSrv "github.com/taubyte/http"
	"github.com/taubyte/p2p/peer"
	"github.com/taubyte/tau/core/services/substrate"
	"github.com/taubyte/tau/core/services/tns"
	"github.com/taubyte/tau/core/vm"
)

type MockedSubstrate interface {
	substrate.Service
}

type mockedSubstrate struct {
	node     peer.Node
	tns      tns.Client
	vm       vm.Service
	http     httpSrv.Service
	smartOps substrate.SmartOpsService
	ctx      context.Context
	ctxC     context.CancelFunc
	branch   string

	// TODO: Should be removed
	counters substrate.CounterService
}

type option func(*mockedSubstrate) error
