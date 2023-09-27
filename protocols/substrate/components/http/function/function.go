package function

import (
	"context"
	"errors"
	"fmt"
	"time"

	goHttp "net/http"

	"github.com/taubyte/go-interfaces/services/substrate/components"
	httpComp "github.com/taubyte/go-interfaces/services/substrate/components/http"
	matcherSpec "github.com/taubyte/go-specs/matcher"
	"github.com/taubyte/tau/protocols/substrate/components/http/common"
	"github.com/taubyte/tau/vm"
	plugins "github.com/taubyte/vm-core-plugins/taubyte"
)

func (f *Function) Provision() (function httpComp.Serviceable, err error) {
	f.instanceCtx, f.instanceCtxC = context.WithCancel(f.srv.Context())
	f.readyCtx, f.readyCtxC = context.WithCancel(f.srv.Context())
	defer func() {
		f.readyError = err
		f.readyDone = true
		f.readyCtxC()
	}()

	if f.Function, err = vm.New(f.instanceCtx, f, f.branch, f.commit); err != nil {
		return nil, fmt.Errorf("initializing wasm module failed with: %w", err)
	}

	cachedFunc, err := f.srv.Cache().Add(f, f.branch)
	if err != nil {
		return nil, fmt.Errorf("adding function to cache failed with: %w", err)
	}
	if f != cachedFunc {
		_f, ok := cachedFunc.(httpComp.Function)
		if ok {
			return _f, nil
		}

		// TODO: Debug Logger if this case is met
	}

	f.provisioned = true
	return f, nil
}

func (f *Function) Handle(w goHttp.ResponseWriter, r *goHttp.Request, matcher components.MatchDefinition) (t time.Time, err error) {
	runtime, pluginApi, err := f.Instantiate()
	if err != nil {
		return t, fmt.Errorf("instantiate failed with: %w", err)
	}
	defer runtime.Close()

	sdk, ok := pluginApi.(plugins.Instance)
	if !ok {
		return t, errors.New("internal: taubyte Plugin is not a plugin instance")
	}

	ev := sdk.CreateHttpEvent(w, r)
	return time.Now(), f.Call(runtime, ev.Id)
}

func (f *Function) Match(matcher components.MatchDefinition) (currentMatchIndex matcherSpec.Index) {
	currentMatch := matcherSpec.NoMatch
	_matcher, ok := matcher.(*common.MatchDefinition)
	if !ok {
		return
	}

	if _matcher.Method == f.config.Method {
		for _, path := range f.config.Paths {
			if path == _matcher.Path {
				currentMatch = matcherSpec.HighMatch
			}
		}
	}

	return currentMatch
}

func (f *Function) Validate(matcher components.MatchDefinition) error {
	if f.Match(matcher) == matcherSpec.NoMatch {
		return errors.New("no match")
	}

	return nil
}
