package taubyte

import (
	"context"
	"testing"

	"github.com/taubyte/tau/core/vm"
)

// typedTestFactory provides its host functions directly (the shape hostfn-gen
// emits), so LoadFactory registers them with no reflection.
type typedTestFactory struct{}

func (f *typedTestFactory) Close() error { return nil }
func (f *typedTestFactory) Name() string { return "typedTestFactory" }

func (f *typedTestFactory) W_double(ctx context.Context, m vm.Module, x uint32) uint32 { return x * 2 }

func (f *typedTestFactory) HostFunctions() []*vm.HostModuleFunctionDefinition {
	return []*vm.HostModuleFunctionDefinition{
		vm.HostFn1("double", f.W_double),
	}
}

var (
	_ vm.Factory              = &typedTestFactory{}
	_ vm.HostFunctionProvider = &typedTestFactory{}
)

// plainFactory implements vm.Factory but not vm.HostFunctionProvider.
type plainFactory struct{}

func (f *plainFactory) Close() error { return nil }
func (f *plainFactory) Name() string { return "plainFactory" }

type mockHostModule struct {
	defs []*vm.HostModuleFunctionDefinition
}

func (m *mockHostModule) Functions(defs ...*vm.HostModuleFunctionDefinition) error {
	m.defs = append(m.defs, defs...)
	return nil
}

func (m *mockHostModule) Memories(...*vm.HostModuleMemoryDefinition) error { return nil }
func (m *mockHostModule) Globals(...*vm.HostModuleGlobalDefinition) error  { return nil }
func (m *mockHostModule) Compile() (vm.ModuleInstance, error)              { return nil, nil }

func TestLoadFactoryTyped(t *testing.T) {
	pi := &pluginInstance{}
	hm := &mockHostModule{}
	if err := pi.LoadFactory(&typedTestFactory{}, hm); err != nil {
		t.Fatal(err)
	}

	if len(hm.defs) != 1 {
		t.Fatalf("expected 1 host function, got %d", len(hm.defs))
	}
	def := hm.defs[0]
	if def.Name != "double" {
		t.Fatalf("name = %q, want double", def.Name)
	}
	if def.Stack == nil {
		t.Fatal("typed def must carry a reflection-free Stack adapter")
	}
	s := []uint64{21}
	def.Stack(context.Background(), nil, s)
	if s[0] != 42 {
		t.Fatalf("double(21) = %d, want 42", s[0])
	}
}

func TestLoadFactoryMissingProvider(t *testing.T) {
	pi := &pluginInstance{}
	hm := &mockHostModule{}
	if err := pi.LoadFactory(&plainFactory{}, hm); err == nil {
		t.Fatal("expected an error for a factory that does not provide host functions")
	}
}
