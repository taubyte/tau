package function

import (
	"context"
	"fmt"

	commonIface "github.com/taubyte/tau/core/services/substrate/components"
	iface "github.com/taubyte/tau/core/services/substrate/components/p2p"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/services/substrate/runtime"
	"github.com/taubyte/tau/services/substrate/runtime/cache"
)

func New(srv iface.Service, config structureSpec.Function, commit, branch string, matcher *iface.MatchDefinition) (commonIface.Serviceable, error) {
	f := &Function{
		srv:     srv,
		config:  config,
		matcher: matcher,
		commit:  commit,
		branch:  branch,
	}

	f.instanceCtx, f.instanceCtxC = context.WithCancel(srv.Context())
	f.readyCtx, f.readyCtxC = context.WithCancel(srv.Context())

	var err error
	defer func() {
		f.readyError = err
		f.readyDone = true
		f.readyCtxC()
	}()

	if f.Function, err = runtime.New(f.instanceCtx, f); err != nil {
		return nil, fmt.Errorf("initializing vm module failed with: %w", err)
	}

	f.assetId, err = cache.ResolveAssetCid(f)
	if err != nil {
		return nil, fmt.Errorf("getting asset id failed with: %w", err)
	}

	_f, err := srv.Cache().Add(f)
	if err != nil {
		return nil, fmt.Errorf("adding P2P function serviceable failed with: %s", err)
	}
	if f.assetId != _f.AssetId() {
		return _f, nil
	}

	if err = f.Validate(matcher); err != nil {
		return nil, fmt.Errorf("validating function with id: `%s` failed with: %s", f.config.Id, err)
	}

	f.serviceConfig, f.serviceApplication, err = srv.LookupService(f.matcher)
	if err != nil {
		return nil, fmt.Errorf("getting service for p2p function with id: `%s` failed with: %s", f.config.Id, err)
	}

	return f, nil
}
