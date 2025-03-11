package mocks

import (
	"context"
	"fmt"

	"github.com/taubyte/tau/core/vm"
	httpMock "github.com/taubyte/tau/pkg/http/mocks"
	"github.com/taubyte/tau/pkg/vm/backend/dfs"
	"github.com/taubyte/tau/pkg/vm/backend/file"
	"github.com/taubyte/tau/pkg/vm/backend/url"
	loader "github.com/taubyte/tau/pkg/vm/loaders/wazero"
	resolver "github.com/taubyte/tau/pkg/vm/resolvers/taubyte"
	vmSrv "github.com/taubyte/tau/pkg/vm/service/wazero"
	source "github.com/taubyte/tau/pkg/vm/sources/taubyte"
	counterSrv "github.com/taubyte/tau/services/substrate/components/counters"
	smartops "github.com/taubyte/tau/services/substrate/components/smartops"
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
