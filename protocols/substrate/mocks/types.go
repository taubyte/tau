package mocks

import (
	"context"

	"github.com/taubyte/go-interfaces/services/substrate"
	"github.com/taubyte/go-interfaces/services/tns"
	"github.com/taubyte/go-interfaces/vm"
	httpSrv "github.com/taubyte/http"
	"github.com/taubyte/p2p/peer"
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
