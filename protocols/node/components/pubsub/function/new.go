package function

import (
	"context"
	"fmt"

	commonIface "github.com/taubyte/go-interfaces/services/substrate/components"
	iface "github.com/taubyte/go-interfaces/services/substrate/components/pubsub"
	structureSpec "github.com/taubyte/go-specs/structure"
	"github.com/taubyte/odo/protocols/node/components/pubsub/common"
	tvm "github.com/taubyte/odo/vm"
)

func New(srv iface.Service, mmi common.MessagingMapItem, config structureSpec.Function, matcher *common.MatchDefinition) (commonIface.Serviceable, error) {
	f := &Function{
		srv:     srv,
		config:  config,
		mmi:     mmi,
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

	_f, err := srv.Cache().Add(f)
	if err != nil {
		return nil, fmt.Errorf("adding pubsub function serviceable failed with: %s", err)
	}
	if f != _f {
		return _f, nil
	}

	err = f.Validate(matcher)
	if err != nil {
		return nil, fmt.Errorf("validating function with id: `%s` failed with: %s", f.config.Id, err)
	}

	f.function = tvm.New(srv, f)
	return f, nil
}
