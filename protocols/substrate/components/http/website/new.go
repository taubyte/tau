package website

import (
	"context"
	"fmt"

	commonIface "github.com/taubyte/go-interfaces/services/substrate/components"
	iface "github.com/taubyte/go-interfaces/services/substrate/components/http"
	"github.com/taubyte/go-interfaces/services/tns"
	"github.com/taubyte/go-specs/extract"
	"github.com/taubyte/tau/protocols/substrate/components/http/common"
	"github.com/taubyte/tau/vm/cache"
)

func New(srv iface.Service, object tns.Object, matcher *common.MatchDefinition) (serviceable commonIface.Serviceable, err error) {
	parser, err := extract.Tns().BasicPath(object.Path().String())
	if err != nil {
		return nil, fmt.Errorf("failed to parse tns path `%s` with: %w", object.Path().String(), err)
	}

	id := parser.Resource()
	w := &Website{
		srv:           srv,
		project:       parser.Project(),
		branch:        parser.Branch(),
		application:   parser.Application(),
		matcher:       matcher,
		commit:        parser.Commit(),
		computedPaths: make(map[string][]string, 0),
	}

	if err = object.Bind(&w.config); err != nil {
		return nil, fmt.Errorf("failed to decode config with: %w", err)
	}
	w.config.Id = id

	w.instanceCtx, w.instanceCtxC = context.WithCancel(srv.Context())
	w.readyCtx, w.readyCtxC = context.WithCancel(srv.Context())
	defer func() {
		w.readyDone = true
		w.readyError = err
		w.readyCtxC()
	}()

	w.assetId, err = cache.ComputeServiceableCid(w, w.branch)
	if err != nil {
		return nil, fmt.Errorf("getting asset id failed with: %w", err)
	}

	_w, err := srv.Cache().Add(w, w.branch)
	if err != nil {
		return nil, fmt.Errorf("adding website serviceable failed with: %s", err)
	}

	w.ctx, w.ctxC = context.WithCancel(w.srv.Context())

	if w != _w {
		return _w, nil
	}

	err = w.getAsset()
	if err != nil {
		return nil, fmt.Errorf("getting website `%s`assets failed with: %s", w.config.Name, err)
	}

	return w, nil
}

func (w *Website) Ready() error {
	if !w.readyDone {
		<-w.readyCtx.Done()
	}

	return w.readyError
}
