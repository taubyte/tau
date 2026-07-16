package taubyte

import (
	"context"
	"testing"

	wazy "github.com/samyfodil/wazy"
	"github.com/taubyte/tau/core/vm"
)

// typedTestFactory registers its host functions with wazy's typed helpers.
type typedTestFactory struct{}

func (f *typedTestFactory) Close() error                                             { return nil }
func (f *typedTestFactory) Name() string                                             { return "typedTestFactory" }
func (f *typedTestFactory) double(ctx context.Context, m vm.Module, x uint32) uint32 { return x * 2 }

func (f *typedTestFactory) RegisterHostFunctions(b wazy.HostModuleBuilder) {
	wazy.HostFunc1(b.NewFunctionBuilder(), f.double).Export("double")
}

var (
	_ vm.Factory              = &typedTestFactory{}
	_ vm.HostFunctionProvider = &typedTestFactory{}
)

// plainFactory implements vm.Factory but not vm.HostFunctionProvider.
type plainFactory struct{}

func (f *plainFactory) Close() error { return nil }
func (f *plainFactory) Name() string { return "plainFactory" }

type mockHostModule struct{ builder wazy.HostModuleBuilder }

func (m *mockHostModule) Builder() wazy.HostModuleBuilder     { return m.builder }
func (m *mockHostModule) Compile() (vm.ModuleInstance, error) { return nil, nil }

func newMockHostModule() *mockHostModule {
	r := wazy.NewRuntime(context.Background())
	return &mockHostModule{builder: r.NewHostModuleBuilder("test")}
}

func TestLoadFactoryProvider(t *testing.T) {
	pi := &pluginInstance{}
	hm := newMockHostModule()
	if err := pi.LoadFactory(&typedTestFactory{}, hm); err != nil {
		t.Fatal(err)
	}
	// registration is deferred to Compile; compiling verifies "double" registered cleanly.
	if _, err := hm.builder.Compile(context.Background()); err != nil {
		t.Fatalf("compile after registration: %v", err)
	}
}

func TestLoadFactoryMissingProvider(t *testing.T) {
	pi := &pluginInstance{}
	if err := pi.LoadFactory(&plainFactory{}, newMockHostModule()); err == nil {
		t.Fatal("expected an error for a factory that does not provide host functions")
	}
}
