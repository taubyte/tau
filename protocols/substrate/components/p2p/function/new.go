package function

import (
	"context"
	"fmt"

	commonIface "github.com/taubyte/go-interfaces/services/substrate/components"
	iface "github.com/taubyte/go-interfaces/services/substrate/components/p2p"
	spec "github.com/taubyte/go-specs/common"
	structureSpec "github.com/taubyte/go-specs/structure"
	tvm "github.com/taubyte/tau/vm"
)

func New(srv iface.Service, config structureSpec.Function, matcher *iface.MatchDefinition) (commonIface.Serviceable, error) {
	f := &Function{
		srv:     srv,
		config:  config,
		matcher: matcher,
	}

	f.instanceCtx, f.instanceCtxC = context.WithCancel(srv.Context())
	f.readyCtx, f.readyCtxC = context.WithCancel(srv.Context())

	var err error
	defer func() {
		f.readyError = err
		f.readyDone = true
		f.readyCtxC()
	}()

	_f, err := srv.Cache().Add(f, spec.DefaultBranch)
	if err != nil {
		return nil, fmt.Errorf("adding P2P function serviceable failed with: %s", err)
	}
	if f != _f {
		return _f, nil
	}

	err = f.Validate(matcher)
	if err != nil {
		return nil, fmt.Errorf("validating function with id: `%s` failed with: %s", f.config.Id, err)
	}

	f.serviceConfig, f.serviceApplication, err = srv.LookupService(f.matcher)
	if err != nil {
		return nil, fmt.Errorf("getting service for p2p function with id: `%s` failed with: %s", f.config.Id, err)
	}

	f.function = tvm.New(srv, f)
	return f, nil
}
