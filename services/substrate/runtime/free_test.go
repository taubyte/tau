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

// Big instances pool until the function's pooled memory reaches
// poolMemBudget; past it they are closed, and taking one frees budget.
func TestPoolMemBudget(t *testing.T) {
	const big = 100 << 20
	f := &Function{
		config:             &structureSpec.Function{Name: "someFunc"},
		serviceable:        newMockServiceable(),
		vmConfig:           &vm.Config{MemoryLimitPages: 4096}, // 256 MiB cap
		availableInstances: make(chan Instance, InstanceMaxRequests),
	}
	free := func() *fakeRuntime {
		rt := &fakeRuntime{size: big}
		if err := (&instance{runtime: rt, parent: f}).Free(); err != nil {
			t.Fatal(err)
		}
		return rt
	}
	for n := 0; n < 2; n++ {
		if rt := free(); rt.closed != 0 {
			t.Fatalf("instance %d within budget must pool", n)
		}
	}
	if rt := free(); rt.closed != 1 {
		t.Fatal("instance over budget must be closed")
	}
	unpool(<-f.availableInstances)
	if rt := free(); rt.closed != 0 {
		t.Fatal("taking a pooled instance must free its budget")
	}
	if got := f.pooledMem.Load(); got != 2*big {
		t.Fatalf("pooledMem = %d, want %d", got, 2*big)
	}
}

// Two functions each under their own budget are closed once together they
// would pass the node budget, and the node total returns to 0 when drained.
func TestNodePoolBudget(t *testing.T) {
	const big = 100 << 20
	defer func(b int64) { nodePoolBudget = b }(nodePoolBudget)
	nodePooledMem.Store(0) // other tests leave instances pooled
	nodePoolBudget = 250 << 20
	newF := func() *Function {
		return &Function{
			config:             &structureSpec.Function{Name: "someFunc"},
			serviceable:        newMockServiceable(),
			vmConfig:           &vm.Config{MemoryLimitPages: 4096},
			availableInstances: make(chan Instance, InstanceMaxRequests),
		}
	}
	free := func(f *Function) int {
		rt := &fakeRuntime{size: big}
		if err := (&instance{runtime: rt, parent: f}).Free(); err != nil {
			t.Fatal(err)
		}
		return rt.closed
	}
	a, b := newF(), newF()
	if free(a) != 0 || free(b) != 0 {
		t.Fatal("instances within the node budget must pool")
	}
	if free(a) != 1 {
		t.Fatal("instance over the node budget must be closed")
	}
	if got := nodePooledMem.Load(); got != 2*big {
		t.Fatalf("nodePooledMem = %d, want %d", got, 2*big)
	}
	unpool(<-a.availableInstances)
	unpool(<-b.availableInstances)
	if got := nodePooledMem.Load(); got != 0 {
		t.Fatalf("nodePooledMem = %d after drain, want 0", got)
	}
	if nodePoolBudgetFor(0) != 512<<20 || nodePoolBudgetFor(256<<20) != 128<<20 || nodePoolBudgetFor(4<<30) != 1<<30 {
		t.Fatal("nodePoolBudgetFor default/floor/quarter wrong")
	}
}

// Shutdown closes every pooled instance and gives its memory back to the node
// budget; an instance returned after Shutdown is closed and not counted.
func TestShutdownReleasesPooledMemory(t *testing.T) {
	f := pooledFunction(t)
	before := nodePooledMem.Load()
	idle := <-f.availableInstances
	f.availableInstances <- idle

	rts := []*fakeRuntime{{size: vm.MemoryPageSize}, {size: vm.MemoryPageSize}}
	for _, rt := range rts {
		if err := (&instance{runtime: rt, parent: f}).Free(); err != nil {
			t.Fatal(err)
		}
	}
	if got := nodePooledMem.Load(); got != before+int64(2*vm.MemoryPageSize) {
		t.Fatalf("nodePooledMem = %d, want %d", got, before+int64(2*vm.MemoryPageSize))
	}

	f.Shutdown()

	if got := nodePooledMem.Load(); got != before {
		t.Fatalf("nodePooledMem = %d after Shutdown, want %d", got, before)
	}
	if got := f.pooledMem.Load(); got != 0 {
		t.Fatalf("pooledMem = %d after Shutdown", got)
	}
	if idle.(*instance).runtime.(*fakeRuntime).closed != 1 || rts[0].closed != 1 || rts[1].closed != 1 {
		t.Fatal("Shutdown must close every pooled instance")
	}

	late := &fakeRuntime{size: vm.MemoryPageSize}
	if err := (&instance{runtime: late, parent: f}).Free(); err != nil {
		t.Fatal(err)
	}
	if late.closed != 1 || nodePooledMem.Load() != before {
		t.Fatalf("an instance back after Shutdown must be closed and not counted, closed=%d", late.closed)
	}
}
