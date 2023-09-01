package function

import (
	"context"
	"fmt"

	commonIface "github.com/taubyte/go-interfaces/services/substrate/components"
	iface "github.com/taubyte/go-interfaces/services/substrate/components/http"
	"github.com/taubyte/go-interfaces/services/tns"
	"github.com/taubyte/go-specs/extract"
	"github.com/taubyte/tau/protocols/substrate/components/http/common"
	"github.com/taubyte/tau/vm"
	"github.com/taubyte/tau/vm/cache"
)

func New(srv iface.Service, object tns.Object, matcher *common.MatchDefinition) (commonIface.Serviceable, error) {
	parser, err := extract.Tns().BasicPath(object.Path().String())
	if err != nil {
		return nil, fmt.Errorf("failed to parse tns path `%s` with: %s", object.Path().String(), err)
	}

	id := parser.Resource()
	f := &Function{
		srv:         srv,
		project:     parser.Project(),
		matcher:     matcher,
		application: parser.Application(),
		commit:      parser.Commit(),
		branch:      parser.Branch(),
	}

	if err = object.Bind(&f.config); err != nil {
		return nil, fmt.Errorf("failed to decode config with: %s", err)
	}

	f.config.Id = id
	f.instanceCtx, f.instanceCtxC = context.WithCancel(srv.Context())
	f.readyCtx, f.readyCtxC = context.WithCancel(srv.Context())
	defer func() {
		f.readyError = err
		f.readyDone = true
		f.readyCtxC()
	}()

	f.assetId, err = cache.ComputeServiceableCid(f, f.branch)
	if err != nil {
		return nil, fmt.Errorf("getting asset id failed with: %w", err)
	}

	if f.dFunc, err = vm.New(f.instanceCtx, f, f.branch, f.commit); err != nil {
		return nil, fmt.Errorf("initializing wasm module failed with: %w", err)
	}

	_f, err := srv.Cache().Add(f, f.branch)
	if err != nil {
		return nil, fmt.Errorf("adding http function serviceable failed with: %s", err)
	}
	if f != _f {
		return _f, nil
	}

	return f, nil
}
