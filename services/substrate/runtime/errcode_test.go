package runtime

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/platformerr"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

type fakeFx func(ctx context.Context) error

func (f fakeFx) RawCall(ctx context.Context, _ ...uint64) ([]uint64, error) { return nil, f(ctx) }

type callModule struct {
	fakeModule
	fx fakeFx
}

func (m callModule) Function(string) (vm.FunctionInstance, error) { return m.fx, nil }

type fakeInstance struct {
	Instance
	module vm.ModuleInstance
	err    error
}

func (i fakeInstance) Module(string) (vm.ModuleInstance, error) { return i.module, i.err }

func callFunction(inst Instance) error {
	f := &Function{
		serviceable:   newMockServiceable(),
		ctx:           context.Background(),
		config:        &structureSpec.Function{Name: "f", Call: "f", Timeout: uint64(20 * time.Millisecond)},
		calls:         new(atomic.Uint64),
		totalCallTime: new(atomic.Int64),
		maxMemory:     new(atomic.Uint64),
	}
	return f.Call(inst, 1)
}

// Each failure answers with its own status, not a blanket 500.
func TestCallErrorStatus(t *testing.T) {
	for name, c := range map[string]struct {
		inst Instance
		want int
	}{
		"module does not load": {fakeInstance{err: errors.New("caching CID `bafy` failed")}, 502},
		"guest runs too long": {fakeInstance{module: callModule{fx: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		}}}, 504},
		"guest traps": {fakeInstance{module: callModule{fx: func(context.Context) error { return errors.New("unreachable") }}}, 500},
	} {
		t.Run(name, func(t *testing.T) {
			err := callFunction(c.inst)
			if got := platformerr.Status(platformerr.CodeOf(err)); err == nil || got != c.want {
				t.Fatalf("%v: status %d, want %d", err, got, c.want)
			}
		})
	}
	if err := callFunction(fakeInstance{module: callModule{fx: func(context.Context) error { return errors.New("trap") }}}); platformerr.CodeOf(err) != platformerr.FunctionError {
		t.Fatalf("a trap is the function's error, got %q", platformerr.CodeOf(err))
	}
}

func TestFullQueueIsNodeBusy(t *testing.T) {
	f := &Function{ctx: context.Background(), shutdown: new(atomic.Bool), instanceReqs: make(chan *instanceRequest)}
	if _, err := f.Instantiate(context.Background()); platformerr.CodeOf(err) != platformerr.NodeBusy {
		t.Fatalf("got %v (%q)", err, platformerr.CodeOf(err))
	}
}
