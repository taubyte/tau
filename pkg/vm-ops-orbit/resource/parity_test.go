package resource

import (
	"reflect"
	"strings"
	"testing"
)

// TestFactoryHostFunctionParity guards the generated HostFunctions() (which
// aggregates embedded providers) against the full set of W_ methods reflection
// would discover — including the ones promoted from embedded resource types
// (*database.Database, *storage.Storage, ...). A new or renamed W_ method that
// isn't regenerated (make gen-hostfn) fails here.
//
// HostFunctions() on a zero-value Factory is safe: it only takes method values
// (x.W_foo), it never invokes them, so the nil embedded pointers are never
// dereferenced.
func TestFactoryHostFunctionParity(t *testing.T) {
	f := &Factory{}

	registered := map[string]bool{}
	for _, def := range f.HostFunctions() {
		registered[def.Name] = true
	}

	rt := reflect.TypeOf(f)
	want := 0
	for i := 0; i < rt.NumMethod(); i++ {
		name := rt.Method(i).Name
		if !strings.HasPrefix(name, "W_") {
			continue
		}
		want++
		if !registered[name[2:]] {
			t.Errorf("W_%s is not registered by HostFunctions(); run `make gen-hostfn`", name[2:])
		}
	}

	if len(registered) != want {
		t.Errorf("registered %d host functions, reflection found %d W_ methods", len(registered), want)
	}
}
