package poe

import (
	"fmt"
	"io/fs"
	"sync"

	"github.com/taubyte/tau/pkg/starlark"
)

const (
	DefaultMaxContexts = 10
)

type Engine interface {
	Score(target string, data map[string]any) (float64, error)
	Check(target string, data map[string]any) (bool, error)
}

type engine struct {
	main       string
	vm         starlark.VM
	filesystem fs.FS

	maxContexts int
	ctxPool     sync.Pool
	ctxSem      chan struct{}
}

func New(filesystem fs.FS, main string) (Engine, error) {
	return NewWithMaxContexts(filesystem, main, DefaultMaxContexts)
}

func NewWithMaxContexts(filesystem fs.FS, main string, maxContexts int) (Engine, error) {
	if maxContexts <= 0 {
		maxContexts = DefaultMaxContexts
	}

	vm, err := starlark.New(filesystem)
	if err != nil {
		return nil, err
	}

	return &engine{
		main:        main,
		vm:          vm,
		filesystem:  filesystem,
		maxContexts: maxContexts,
		ctxSem:      make(chan struct{}, maxContexts),
		ctxPool:     sync.Pool{},
	}, nil
}

type contextWrapper struct {
	ctx starlark.Context
	err error
}

func (e *engine) getContext() (starlark.Context, error) {
	if wrapper := e.ctxPool.Get(); wrapper != nil {
		w := wrapper.(*contextWrapper)
		if w.err != nil {
			return nil, w.err
		}
		e.ctxSem <- struct{}{}
		return w.ctx, nil
	}

	e.ctxSem <- struct{}{}
	ctx, err := e.vm.File(e.main)
	if err != nil {
		<-e.ctxSem
		return nil, err
	}
	return ctx, nil
}

func (e *engine) putContext(ctx starlark.Context, err error) {
	<-e.ctxSem

	if err != nil {
		return
	}

	e.ctxPool.Put(&contextWrapper{ctx: ctx, err: nil})
}

func (e *engine) Score(target string, data map[string]any) (float64, error) {
	ctx, err := e.getContext()
	if err != nil {
		return 0, err
	}
	defer e.putContext(ctx, err)

	result, err := ctx.CallWithNative("score", target, data)
	if err != nil {
		return 0, err
	}

	score, ok := result.(float64)
	if !ok {
		return 0, fmt.Errorf("score function did not return a float64, got %T", result)
	}

	// Validate score is between 0 and 1
	if score < 0 || score > 1 {
		return 0, fmt.Errorf("score must be between 0 and 1, got %f", score)
	}

	return score, nil
}

func (e *engine) Check(target string, data map[string]any) (bool, error) {
	ctx, err := e.getContext()
	if err != nil {
		return false, err
	}
	defer e.putContext(ctx, err)

	result, err := ctx.CallWithNative("check", target, data)
	if err != nil {
		return false, err
	}

	check, ok := result.(bool)
	if !ok {
		return false, fmt.Errorf("check function did not return a bool, got %T", result)
	}

	return check, nil
}
