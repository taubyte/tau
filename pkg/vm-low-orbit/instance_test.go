package taubyte

import (
	"testing"

	"github.com/taubyte/tau/core/vm"
)

type wDiscoveryTestFactory struct{}

func (f *wDiscoveryTestFactory) Close() error { return nil }
func (f *wDiscoveryTestFactory) Name() string { return "wDiscoveryTestFactory" }

func (f *wDiscoveryTestFactory) W_hello() string      { return "hello" }
func (f *wDiscoveryTestFactory) W_double(x int) int   { return x * 2 }
func (f *wDiscoveryTestFactory) AlsoNotPrefixed() int { return 0 }

var _ vm.Factory = &wDiscoveryTestFactory{}

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

func TestLoadFactoryWDiscovery(t *testing.T) {
	pi := &pluginInstance{}

	hm := &mockHostModule{}
	if err := pi.LoadFactory(&wDiscoveryTestFactory{}, hm); err != nil {
		t.Fatal(err)
	}

	if len(hm.defs) != 2 {
		t.Fatalf("expected 2 discovered W_ methods, got %d", len(hm.defs))
	}

	names := make(map[string]*vm.HostModuleFunctionDefinition)
	for _, def := range hm.defs {
		names[def.Name] = def
	}

	helloDef, ok := names["hello"]
	if !ok {
		t.Fatal("expected `hello` (stripped from W_hello) to be discovered")
	}
	helloHandler, ok := helloDef.Handler.(func() string)
	if !ok {
		t.Fatalf("hello handler has unexpected type %T", helloDef.Handler)
	}
	if got := helloHandler(); got != "hello" {
		t.Fatalf("hello handler returned %q, expected %q", got, "hello")
	}

	doubleDef, ok := names["double"]
	if !ok {
		t.Fatal("expected `double` (stripped from W_double) to be discovered")
	}
	doubleHandler, ok := doubleDef.Handler.(func(int) int)
	if !ok {
		t.Fatalf("double handler has unexpected type %T", doubleDef.Handler)
	}
	if got := doubleHandler(21); got != 42 {
		t.Fatalf("double handler returned %d, expected %d", got, 42)
	}

	if _, ok := names["AlsoNotPrefixed"]; ok {
		t.Fatal("AlsoNotPrefixed must not be discovered: no W_ prefix")
	}
}

// TestLoadFactoryWDiscoveryCacheHit exercises a second LoadFactory call for the
// same factory type (a different instance), which should hit the cached
// discovery and still produce identical, correctly-bound defs.
func TestLoadFactoryWDiscoveryCacheHit(t *testing.T) {
	pi := &pluginInstance{}

	hm1 := &mockHostModule{}
	if err := pi.LoadFactory(&wDiscoveryTestFactory{}, hm1); err != nil {
		t.Fatal(err)
	}

	hm2 := &mockHostModule{}
	if err := pi.LoadFactory(&wDiscoveryTestFactory{}, hm2); err != nil {
		t.Fatal(err)
	}

	if len(hm1.defs) != len(hm2.defs) {
		t.Fatalf("cache hit produced a different number of defs: %d vs %d", len(hm1.defs), len(hm2.defs))
	}

	for i := range hm1.defs {
		if hm1.defs[i].Name != hm2.defs[i].Name {
			t.Fatalf("cache hit produced defs in different order/names at %d: %s vs %s", i, hm1.defs[i].Name, hm2.defs[i].Name)
		}
	}

	doubleDef, ok := (map[string]*vm.HostModuleFunctionDefinition{hm2.defs[0].Name: hm2.defs[0], hm2.defs[1].Name: hm2.defs[1]})["double"]
	if !ok {
		t.Fatal("expected `double` to be discovered on cache hit")
	}
	doubleHandler := doubleDef.Handler.(func(int) int)
	if got := doubleHandler(10); got != 20 {
		t.Fatalf("double handler (cache hit) returned %d, expected %d", got, 20)
	}
}
