package mocks

import (
	"context"
	"fmt"

	"github.com/taubyte/go-interfaces/vm"
	httpMock "github.com/taubyte/http/mocks"
	counterSrv "github.com/taubyte/odo/protocols/substrate/components/counters"
	smartops "github.com/taubyte/odo/protocols/substrate/components/smartops"
	"github.com/taubyte/vm/backend/dfs"
	"github.com/taubyte/vm/backend/file"
	"github.com/taubyte/vm/backend/url"
	loader "github.com/taubyte/vm/loaders/wazero"
	resolver "github.com/taubyte/vm/resolvers/taubyte"
	vmSrv "github.com/taubyte/vm/service/wazero"
	source "github.com/taubyte/vm/sources/taubyte"
)

func New(ctx context.Context, ops ...option) (MockedSubstrate, error) {
	service := &mockedSubstrate{}

	for _, op := range ops {
		if err := op(service); err != nil {
			return nil, fmt.Errorf("running options failed with: %w", err)
		}
	}

	if service.node == nil || service.tns == nil {
		return nil, fmt.Errorf("node %#v, or tns %#v is nil", service.node, service.tns)
	}

	if ctx == nil {
		ctx = context.Background()
	}

	if len(service.branch) < 1 {
		service.branch = "master"
	}

	service.ctx, service.ctxC = context.WithCancel(ctx)

	if service.http == nil {
		service.http = httpMock.NewUnimplemented(service.ctx)
	}

	var err error
	service.counters, err = counterSrv.New(service)
	if err != nil {
		return nil, fmt.Errorf("starting counter service failed with: %w", err)
	}

	service.smartOps, err = smartops.New(service)
	if err != nil {
		return nil, fmt.Errorf("starting smartops service failed with: %w", err)
	}

	backends := []vm.Backend{
		dfs.New(service.node),
		file.New(),
		url.New(),
	}

	rslv := resolver.New(service.tns)
	loader := loader.New(rslv, backends...)
	src := source.New(loader)
	service.vm = vmSrv.New(ctx, src)

	return service, nil
}
