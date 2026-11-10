package service

import (
	"strings"
	"testing"

	servicesCommon "github.com/taubyte/tau/services/common"
)

func runChunk(run string, off int64, data string, done bool) *servicesCommon.LiveLogChunk {
	return &servicesCommon.LiveLogChunk{Job: "j", Run: run, Offset: off, Data: []byte(data), Done: done}
}

// A monkey dies mid-job and another runs it: same job id, offsets start over.
func TestLiveLogs_JobRunsAgainOnAnotherMonkey(t *testing.T) {
	ll := &liveLogs{jobs: map[string]*liveLog{}}
	ll.add(runChunk("first", 0, "cloning ", false))
	ll.add(runChunk("first", 8, "building", false))
	l := ll.jobs["j"]
	changed := l.changed

	ll.add(runChunk("second", 0, "cloning again", false))
	select {
	case <-changed:
	default:
		t.Fatal("followers not woken")
	}

	// the dead monkey's chunks still in flight are not mixed in
	ll.add(runChunk("first", 16, "late", false))
	ll.add(runChunk("first", 20, "", true))
	if l.done {
		t.Fatal("the dead run ended the log")
	}

	ll.add(runChunk("second", 13, " done", false))
	ll.add(runChunk("second", 18, "", true))

	got := string(l.data)
	if !l.done || !strings.HasPrefix(got, "cloning building") || !strings.Contains(got, "started again") || !strings.HasSuffix(got, "cloning again done") || strings.Contains(got, "late") {
		t.Fatalf("done=%v got %q", l.done, got)
	}
}

func TestLiveLogs_RunsAreBounded(t *testing.T) {
	ll := &liveLogs{jobs: map[string]*liveLog{}}
	for i := 0; i < liveLogsMaxRuns+5; i++ {
		ll.add(runChunk(strings.Repeat("r", i+1), 0, "x", false))
	}
	if n := len(ll.jobs["j"].oldRun); n > liveLogsMaxRuns {
		t.Fatalf("%d runs", n)
	}
}
