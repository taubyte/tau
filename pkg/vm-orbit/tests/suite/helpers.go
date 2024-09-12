package suite

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/taubyte/tau/core/vm"
	vmPlugin "github.com/taubyte/tau/pkg/vm-orbit/satellite/vm"
	fileBE "github.com/taubyte/tau/pkg/vm/backend/file"
	vmContext "github.com/taubyte/tau/pkg/vm/context"
	loader "github.com/taubyte/tau/pkg/vm/loaders/wazero"
	resolver "github.com/taubyte/tau/pkg/vm/resolvers/taubyte"
	service "github.com/taubyte/tau/pkg/vm/service/wazero"
	source "github.com/taubyte/tau/pkg/vm/sources/taubyte"
	"github.com/taubyte/utils/id"
	"gotest.tools/v3/assert"
)

// suite wraps methods used to test a wasm module with injected plugins, locally
type suite struct {
	ctx      context.Context
	ctxC     context.CancelFunc
	instance vm.Instance
	runtime  vm.Runtime
}

// module wraps methods to call module functions
type module struct {
	suite *suite
	mI    vm.ModuleInstance
}

// New creates a new plugin testing suite
func New(ctx context.Context) (*suite, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	var ctxC context.CancelFunc
	ctx, ctxC = context.WithCancel(ctx)

	rslver := resolver.New(nil)
	ldr := loader.New(rslver, fileBE.New())
	src := source.New(ldr)
	vmService := service.New(ctx, src)

	vmCtx, err := vmContext.New(
		ctx,
		vmContext.Application(id.Generate()),
		vmContext.Project(id.Generate()),
		vmContext.Resource(id.Generate()),
		vmContext.Branch("master"),
		vmContext.Commit("head_commit"),
	)
	if err != nil {
		ctxC()
		return nil, fmt.Errorf("creating new vm context failed with: %w", err)
	}

	instance, err := vmService.New(vmCtx, vm.Config{})
	if err != nil {
		ctxC()
		return nil, fmt.Errorf("creating new vm instance failed with: %w", err)
	}

	rt, err := instance.Runtime(nil)
	if err != nil {
		ctxC()
		return nil, fmt.Errorf("creating new vm runtime failed with: %w", err)
	}

	return &suite{
		instance: instance,
		runtime:  rt,
		ctx:      ctx,
		ctxC:     ctxC,
	}, nil
}

// AttachPlugin attaches a built plugin onto the testing suite
func (s *suite) AttachPlugin(plugin vm.Plugin) error {
	if _, _, err := s.runtime.Attach(plugin); err != nil {
		return fmt.Errorf("attaching plugin `%s` failed with: %w", plugin.Name(), err)
	}

	return nil
}

// AttachPluginFromPath builds a plugin from a given plugin binary path, then attaches it to the testing suite
func (s *suite) AttachPluginFromPath(filename string) error {
	plugin, err := vmPlugin.Load(filename, s.ctx)
	if err != nil {
		return fmt.Errorf("loading plugin `%s` failed with: %w", filename, err)
	}

	if _, _, err = s.runtime.Attach(plugin); err != nil {
		return fmt.Errorf("attaching plugin `%s` failed with: %w", plugin.Name(), err)
	}

	return nil
}

// Close will close all dependencies of the testing suite
func (s *suite) Close() {
	s.runtime.Close()
	s.instance.Close()
	s.ctxC()
}

// WasmModule creates a module from the given wasmfile path used to calling exported methods
func (s *suite) WasmModule(filename string) (*module, error) {
	mod, err := s.runtime.Module("/file/" + filename)
	if err != nil {
		return nil, fmt.Errorf("creating new module instance failed with: %w", err)
	}

	return &module{
		suite: s,
		mI:    mod,
	}, nil
}

// Call will call an exported method from the the module
func (m *module) Call(ctx context.Context, function string, args ...interface{}) (vm.Return, error) {
	fI, err := m.mI.Function(function)
	if err != nil {
		return nil, fmt.Errorf("getting function `%s` failed with: ", err)
	}

	ret := fI.Call(ctx, args...)
	if ret.Error() != nil {
		return nil, fmt.Errorf("calling `%s` failed with: %w (ctx.err=%w)", function, ret.Error(), ctx.Err())
	}

	return ret, nil
}

// Debug is intended to be used after Call() has been invoked, used to copy the wasm runtime stdOut and stdErr
func (m *module) Debug() {
	io.Copy(os.Stdout, m.suite.instance.Stdout())
	io.Copy(os.Stderr, m.suite.instance.Stderr())
}

// Debug is intended to be used after Call() has been invoked, used to copy the wasm runtime stdOut and stdErr
func (m *module) AssetOutput(t *testing.T, value string) {
	var b bytes.Buffer
	io.Copy(&b, m.suite.instance.Stdout())
	assert.Equal(t, value, b.String())
}

func (m *module) AssetErrorOutput(t *testing.T, value string) {
	var b bytes.Buffer
	io.Copy(&b, m.suite.instance.Stderr())
	assert.Equal(t, value, b.String())
}

// Returns arguments to be appended to a builder.Plugin call for adding build tags
func GoBuildTags(tags ...string) []string {
	args := []string{"-tags"}
	args = append(args, tags...)
	return args
}
