package vm

import (
	"context"
	"io"
	"sync"

	"github.com/samyfodil/wazy"
	"github.com/samyfodil/wazy/api"
	"github.com/spf13/afero"
	"github.com/taubyte/tau/core/vm"
)

/*************** Function Instance ***************/

type funcInstance struct {
	module   *moduleInstance
	function api.Function
}

/*************** Host Module ***************/

type functionDef struct {
	fn      func(ctx context.Context, mod api.Module, stack []uint64)
	params  []vm.ValueType
	results []vm.ValueType
}

type memoryPages struct {
	min   uint64
	max   uint64
	maxed bool
}

type hostModule struct {
	ctx       vm.Context
	name      string
	runtime   *runtime
	functions map[string]functionDef
	memories  map[string]memoryPages
	globals   map[string]interface{}
}

/*************** Instance ***************/

type instance struct {
	ctx       vm.Context
	service   vm.Service
	lock      sync.RWMutex
	fs        afero.Fs
	config    *vm.Config
	output    io.ReadWriteCloser
	outputErr io.ReadWriteCloser
	deps      map[string]vm.SourceModule
}

/*************** Module Instance ***************/
type moduleInstance struct {
	parent *runtime
	module api.Module
	ctx    context.Context
}

/*************** Runtime ***************/

type runtime struct {
	instance *instance
	modules  map[string]api.Module

	runtime wazy.Runtime

	wasiStartDone chan bool
}

/*************** Service ***************/

type service struct {
	ctx    context.Context
	ctxC   context.CancelFunc
	source vm.Source
}
