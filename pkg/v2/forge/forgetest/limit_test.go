package forgetest

import (
	"os"
	"runtime/debug"
	"testing"
)

// A SOFT MEMORY LIMIT IS PART OF THIS SUITE, not a property of whoever runs it.
//
// TestTheLedgerHolds type-checks the whole forge tree with Tests:true and full
// TypesInfo, and then builds a counterfactual run per fault on top of that. Its
// LIVE set is about 2.9 GB and no arrangement of the code moves it: freeing the
// tree the moment its one consumer has reduced it to a []string changed peak
// RSS by 3% (4.07 GB -> 3.96 GB), because the peak is during the load and not
// after it. Measured, both.
//
// What does move is the headroom Go's GC takes above that live set. Left alone
// the collector grows the heap until the package peaks near 4 GB and is
// OOM-killed inside a 4 GB cgroup — which is what scripts/cap now gives every
// test run, and what CI should give it too. Measured over the whole package:
//
//	none    killed at 4G (SIGKILL, no diagnostic)
//	3 GiB   3.34 GB   22.3s
//	2 GiB   3.31 GB   14.3s
//	1 GiB   3.32 GB   17.9s
//
// So 2 GiB, and the cost is bounded: below it the collector runs continuously
// against a live set it cannot get under, and the wall time is what pays.
//
// It is set here rather than in the runner's environment because a test that
// only passes when invoked a particular way is a test that fails for the next
// person. GOMEMLIMIT still wins if someone sets it — SetMemoryLimit is the
// same knob, and the env var is read after this returns only in the sense that
// a caller who set one meant it, so honour theirs.
func TestMain(m *testing.M) {
	if os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(2 << 30)
	}
	os.Exit(m.Run())
}
