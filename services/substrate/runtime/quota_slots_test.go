package runtime

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/taubyte/tau/core/vm"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

func instancesOf(project string) int {
	projectLoad.mu.Lock()
	defer projectLoad.mu.Unlock()
	if l := projectLoad.m[project]; l != nil {
		return l.instances
	}
	return 0
}

// pooledFunction runs a function's instance manager with one idle instance in its pool.
func pooledFunction(t *testing.T) *Function {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	f := &Function{
		ctx:                ctx,
		config:             &structureSpec.Function{Name: "someFunc"},
		serviceable:        newMockServiceable(),
		vmConfig:           &vm.Config{MemoryLimitPages: 4},
		instanceReqs:       make(chan *instanceRequest, InstanceMaxRequests),
		availableInstances: make(chan Instance, InstanceMaxRequests),
		shutdown:           new(atomic.Bool),
		shutdownDone:       make(chan struct{}),
	}
	f.availableInstances <- &instance{runtime: &fakeRuntime{size: vm.MemoryPageSize}, parent: f, pooled: true}
	go f.intanceManager()
	return f
}

// A checked-out instance holds a project slot; returning it to the pool frees the slot.
func TestCheckedOutInstanceHoldsSlot(t *testing.T) {
	f := pooledFunction(t)
	project := f.serviceable.Project()
	before := instancesOf(project)

	inst, err := f.Instantiate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := instancesOf(project); got != before+1 {
		t.Fatalf("checked-out instance must hold a slot: %d -> %d", before, got)
	}
	if err := inst.Free(); err != nil {
		t.Fatal(err)
	}
	if got := instancesOf(project); got != before {
		t.Fatalf("an idle pooled instance must not hold a slot: %d -> %d", before, got)
	}
}

// Free on a full pool closes the instance instead of blocking.
func TestFreeOnFullPoolCloses(t *testing.T) {
	f := &Function{
		config:             &structureSpec.Function{Name: "someFunc"},
		serviceable:        newMockServiceable(),
		vmConfig:           &vm.Config{MemoryLimitPages: 4},
		availableInstances: make(chan Instance, 1),
	}
	f.availableInstances <- &instance{runtime: &fakeRuntime{}, parent: f}
	rt := &fakeRuntime{size: vm.MemoryPageSize}
	done := make(chan error, 1)
	go func() { done <- (&instance{runtime: rt, parent: f}).Free() }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Free blocked on a full pool")
	}
	if rt.closed != 1 {
		t.Fatalf("the instance must be closed, closed=%d", rt.closed)
	}
}

// A caller that left before its instance arrived does not strand it: the
// instance goes back to the pool and its slot is released.
func TestInstanceForGoneCallerIsReturned(t *testing.T) {
	f := pooledFunction(t)
	project := f.serviceable.Project()
	before := instancesOf(project)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.Instantiate(ctx); err == nil {
		t.Fatal("a cancelled caller gets an error")
	}

	// let the manager take the request and settle what it pulled from the pool
	for len(f.instanceReqs) != 0 {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	if len(f.availableInstances) != 1 {
		t.Fatal("the undelivered instance must go back to the pool")
	}
	if got := instancesOf(project); got != before {
		t.Fatalf("the undelivered instance must release its slot: %d -> %d", before, got)
	}
}
