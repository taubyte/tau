package vm

import (
	"path/filepath"
	goruntime "runtime"
	"testing"
	"time"

	"github.com/taubyte/tau/core/vm"
)

// Production 2026-11-07 (node .169): substrate shares one long-lived vm.Context
// per function, and New left a goroutine per instance waiting on that context,
// holding the instance (and its output files) until the function was unloaded.
// A closed or failed instance must release its goroutine and /tmp/vm files.
func TestInstanceClose_ReleasesGoroutineAndTempFiles(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	ctx, service, err := newService() // ctx is never cancelled, like a function's
	if err != nil {
		t.Fatal(err)
	}

	before := settledGoroutines()
	for i := 0; i < 50; i++ {
		inst, err := service.New(ctx, vm.Config{}) // default output: temp-file pipes
		if err != nil {
			t.Fatal(err)
		}
		if err = inst.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if grew := settledGoroutines() - before; grew > 5 {
		t.Fatalf("50 closed instances left %d goroutines", grew)
	}
	if left, _ := filepath.Glob(filepath.Join(tmp, "vm*")); len(left) != 0 {
		t.Fatalf("closed instances left %d temp files", len(left))
	}
}

// settledGoroutines waits for the goroutine count to stop changing, so
// setup goroutines winding down do not hide a leak.
func settledGoroutines() int {
	n := goruntime.NumGoroutine()
	for i := 0; i < 50; i++ {
		time.Sleep(100 * time.Millisecond)
		m := goruntime.NumGoroutine()
		if m == n {
			return m
		}
		n = m
	}
	return n
}
