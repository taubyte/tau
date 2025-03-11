package mocks

import (
	"context"

	"github.com/taubyte/tau/core/services/substrate"
	"github.com/taubyte/tau/core/services/tns"
	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/p2p/peer"
	httpSrv "github.com/taubyte/tau/pkg/http"
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
