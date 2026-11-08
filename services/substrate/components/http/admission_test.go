package http

import (
	"errors"
	"runtime"
	"testing"

	"github.com/taubyte/tau/pkg/platformerr"
)

func TestMaxInFlightScalesWithMachine(t *testing.T) {
	const GB = 1 << 30
	for _, c := range []struct {
		cpus int
		mem  uint64
		want int
	}{
		{2, 2 * GB, 128}, {4, 2 * GB, 256}, {16, 2 * GB, 256}, {2, 64 * GB, 128}, {2, 512 << 20, 64}, {1, 0, 1},
	} {
		if got := maxInFlight(c.cpus, c.mem); got != c.want {
			t.Errorf("maxInFlight(%d, %d) = %d, want %d", c.cpus, c.mem, got, c.want)
		}
	}
}

func TestAdmittedLimitsAndReleases(t *testing.T) {
	slots := make(chan struct{}, 1)
	hold, release := make(chan struct{}), make(chan struct{})
	go admitted(slots, func() error { close(hold); <-release; return nil })
	<-hold

	called := false
	err := admitted(slots, func() error { called = true; return nil })
	if platformerr.CodeOf(err) != platformerr.NodeBusy || called {
		t.Fatalf("past the limit: err %v, ran %v", err, called)
	}
	close(release)
	for len(slots) != 0 { // the holder frees its slot on return
		runtime.Gosched()
	}

	if err := admitted(slots, func() error { return nil }); err != nil || len(slots) != 0 {
		t.Fatalf("after success: %v, %d held", err, len(slots))
	}
	if err := admitted(slots, func() error { return errors.New("x") }); err == nil || len(slots) != 0 {
		t.Fatalf("after error: %v, %d held", err, len(slots))
	}
	func() {
		defer func() { recover() }()
		admitted(slots, func() error { panic("boom") })
	}()
	if len(slots) != 0 {
		t.Fatal("slot leaked after panic")
	}
}
