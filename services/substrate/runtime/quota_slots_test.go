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
