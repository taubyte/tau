package function

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	goHttp "net/http"

	"github.com/ipfs/go-log/v2"
	"github.com/taubyte/tau/clients/p2p/seer/usage"
	"github.com/taubyte/tau/core/services/substrate/components"
	httpComp "github.com/taubyte/tau/core/services/substrate/components/http"
	matcherSpec "github.com/taubyte/tau/pkg/specs/matcher"
	"github.com/taubyte/tau/services/substrate/components/http/common"
	"github.com/taubyte/tau/services/substrate/components/metrics"
	"github.com/taubyte/tau/services/substrate/runtime"
)

func (f *Function) Provision() (function httpComp.Serviceable, err error) {
	f.instanceCtx, f.instanceCtxC = context.WithCancel(f.srv.Context())
	f.readyCtx, f.readyCtxC = context.WithCancel(f.srv.Context())
	defer func() {
		f.readyError = err
		f.readyDone = true
		f.readyCtxC()
	}()

	cachedFunc, err := f.srv.Cache().Add(f)
	if err != nil {
		return nil, fmt.Errorf("adding function to cache failed with: %w", err)
	}

	if f != cachedFunc {
		_f, ok := cachedFunc.(httpComp.Function)
		if ok {
			return _f, nil
		}
	}

	if f.Function, err = runtime.New(f.instanceCtx, f); err != nil {
		return nil, fmt.Errorf("initializing wasm module failed with: %w", err)
	}

	f.metrics.Cached = 1
	f.provisioned = true

	return f, nil
}

func (f *Function) Handle(w goHttp.ResponseWriter, r *goHttp.Request, matcher components.MatchDefinition) (t time.Time, err error) {
	instance, err := f.Instantiate(f.instanceCtx)
	if err != nil {
		return t, fmt.Errorf("instantiate failed with: %w", err)
	}
	defer instance.Free()

	ev := instance.SDK().CreateHttpEvent(w, r)

	return time.Now(), f.Call(instance, ev.Id)
}

var logger = log.Logger("tau.substrate.components.http.function")

func (f *Function) Metrics() *metrics.Function {
	m := f.metrics

	maxMemory := f.config.Memory
	if f.provisioned {
		m.AvgRunTime = f.CallTime().Nanoseconds()
		m.ColdStart = f.ColdStart().Nanoseconds()
		maxMemory = f.MemoryMax()
	}

	// Memory == 0 no memory limit
	if maxMemory <= 0 {
		maxMemory = WasmMemorySizeLimit
	}

	if mem, err := usage.GetMemoryUsage(); err != nil {
		logger.Errorf("getting memory usage failed with: %s", err.Error())
	} else {
		m.Memory = float64(mem.Free) / float64(maxMemory)
	}

	return &m
}

func (f *Function) Match(matcher components.MatchDefinition) (currentMatchIndex matcherSpec.Index) {
	currentMatch := matcherSpec.NoMatch
	_matcher, ok := matcher.(*common.MatchDefinition)
	if !ok {
		return
	}

	// The request's method stays exact — RFC 9110 §9.1 makes the method token
	// case-sensitive, so a client sending "get" has not sent GET. Only the
	// CONFIGURED method is canonicalized, because that side was typed by a
	// human and the DSL accepts any casing for it (IsHttpMethod upper-cases
	// before checking, and advertises the uppercase set as canonical). Without
	// this, a function authored `method: get` validated, compiled, deployed —
	// and silently never routed.
	if _matcher.Method == strings.ToUpper(f.config.Method) {
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
