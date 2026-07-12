package runtime

import (
	"testing"

	"github.com/taubyte/tau/core/vm"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

func TestShouldRetire(t *testing.T) {
	const capBytes = uint64(300000) // 2/3 of capBytes == 200000

	for name, test := range map[string]struct {
		useMem uint32
		want   bool
	}{
		"under two thirds of cap": {
			useMem: 150000,
			want:   false,
		},
		// The threshold is a strict `>`: sitting exactly on two thirds is kept.
		"exactly two thirds of cap": {
			useMem: 200000,
			want:   false,
		},
		"past two thirds of cap": {
			useMem: 250000,
			want:   true,
		},
		// A module whose footprint fills the cap has no growth headroom;
		// pooling it would only defer a mid-call OOM trap, so it cold-starts
		// every call by design.
		"at cap": {
			useMem: 300000,
			want:   true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := shouldRetire(test.useMem, capBytes); got != test.want {
				t.Errorf("shouldRetire(%d, %d) = %v, want %v", test.useMem, capBytes, got, test.want)
			}
		})
	}
}

// fakeMemory/fakeModule/fakeRuntime are minimal vm fakes exposing only the
// surface Free() touches: a single module whose reported size we control.
type fakeMemory struct {
	vm.Memory
	size uint32
}

func (m fakeMemory) Size() uint32 { return m.size }

type fakeModule struct {
	vm.ModuleInstance
	size uint32
}

func (m fakeModule) Memory() vm.Memory { return fakeMemory{size: m.size} }

type fakeRuntime struct {
	vm.Runtime
	size   uint32
	closed int
}

func (r *fakeRuntime) Modules() []string { return []string{"m"} }

func (r *fakeRuntime) Module(name string) (vm.ModuleInstance, error) {
	return fakeModule{size: r.size}, nil
}

func (r *fakeRuntime) Close() error {
	r.closed++
	return nil
}

// TestInstanceFree exercises the stateful Free() contract: healthy instances
// are pooled, instances grown past two thirds of the enforced page cap are
// retired (closed, not repooled — the old code leaked them), and instances
// flagged failed are retired regardless of memory.
func TestInstanceFree(t *testing.T) {
	const pages = 4 // cap = 4 * 65536 = 262144 bytes, 2/3 == 174762

	newInst := func(rt *fakeRuntime) (*instance, *Function) {
		f := &Function{
			config:             &structureSpec.Function{Name: "someFunc"},
			serviceable:        newMockServiceable(),
			vmConfig:           &vm.Config{MemoryLimitPages: pages},
			availableInstances: make(chan Instance, InstanceMaxRequests),
		}
		return &instance{runtime: rt, parent: f}, f
	}

	pooled := func(f *Function) bool {
		select {
		case <-f.availableInstances:
			return true
		default:
			return false
		}
	}

	t.Run("healthy instance is pooled", func(t *testing.T) {
		rt := &fakeRuntime{size: 2 * vm.MemoryPageSize}
		i, f := newInst(rt)
		if err := i.Free(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rt.closed != 0 || !pooled(f) {
			t.Fatalf("instance should be pooled, closed=%d", rt.closed)
		}
	})

	t.Run("instance past two thirds of cap is retired", func(t *testing.T) {
		rt := &fakeRuntime{size: 3 * vm.MemoryPageSize}
		i, f := newInst(rt)
		if err := i.Free(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rt.closed != 1 || pooled(f) {
			t.Fatalf("instance should be retired, closed=%d", rt.closed)
		}
		// retired without ever pooling → the no-headroom config warning fires
		if !f.noPoolWarned.Load() {
			t.Fatal("expected never-pooled retirement to set noPoolWarned")
		}
	})

	t.Run("growth retirement after pooling does not warn", func(t *testing.T) {
		rt := &fakeRuntime{size: 2 * vm.MemoryPageSize}
		i, f := newInst(rt)
		if err := i.Free(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !pooled(f) {
			t.Fatal("instance should be pooled")
		}
		rt.size = 3 * vm.MemoryPageSize
		if err := i.Free(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rt.closed != 1 {
			t.Fatalf("grown instance should be retired, closed=%d", rt.closed)
		}
		if f.noPoolWarned.Load() {
			t.Fatal("growth retirement after pooling must not warn")
		}
	})

	t.Run("failed instance is retired regardless of memory", func(t *testing.T) {
		rt := &fakeRuntime{size: vm.MemoryPageSize}
		i, f := newInst(rt)
		i.failed = true
		if err := i.Free(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rt.closed != 1 || pooled(f) {
			t.Fatalf("failed instance should be retired, closed=%d", rt.closed)
		}
	})
}
