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

type hostModule struct {
	ctx     vm.Context
	builder wazy.HostModuleBuilder
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
