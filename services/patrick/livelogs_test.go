package service

import (
	"strings"
	"testing"

	servicesCommon "github.com/taubyte/tau/services/common"
)

func chunk(off int64, data string, done bool) *servicesCommon.LiveLogChunk {
	return &servicesCommon.LiveLogChunk{Job: "j", Offset: off, Data: []byte(data), Done: done}
}

func TestLiveLogs_OutOfOrder(t *testing.T) {
	ll := &liveLogs{jobs: map[string]*liveLog{}}
	ll.add(chunk(6, "world", false))
	if got := string(ll.jobs["j"].data); got != "" {
		t.Fatalf("took a chunk ahead of the log: %q", got)
	}
	ll.add(chunk(0, "hello ", false))
	ll.add(chunk(6, "world", false)) // duplicate
	ll.add(chunk(11, "", true))

	l := ll.jobs["j"]
	if got := string(l.data); got != "hello world" || !l.done {
		t.Fatalf("got %q done=%v", got, l.done)
	}
}

func TestLiveLogs_GapIsSkipped(t *testing.T) {
	ll := &liveLogs{jobs: map[string]*liveLog{}}
	// Patrick started mid-job: the first chunks never arrive
	for i := 0; i <= liveLogsMaxPending; i++ {
		ll.add(chunk(int64(100+i), "x", false))
	}

	l := ll.jobs["j"]
	if !strings.Contains(string(l.data), "not received") || !strings.HasSuffix(string(l.data), strings.Repeat("x", liveLogsMaxPending+1)) {
		t.Fatalf("gap not skipped: %q", l.data)
	}
}

func TestLiveLogs_DoneEndsALogMissingItsTail(t *testing.T) {
	ll := &liveLogs{jobs: map[string]*liveLog{}}
	ll.add(chunk(0, "start", false))
	ll.add(chunk(50, "", true))

	l := ll.jobs["j"]
	if l.done {
		t.Fatal("ended without waiting for late chunks")
	}
	ll.finish(l)
	if !l.done || !strings.Contains(string(l.data), "end of this log") {
		t.Fatalf("got %q done=%v", l.data, l.done)
	}
}

func TestLiveLogs_LastChunkAfterTheEnd(t *testing.T) {
	ll := &liveLogs{jobs: map[string]*liveLog{}}
	ll.add(chunk(0, "start ", false))
	ll.add(chunk(10, "", true))
	ll.add(chunk(6, "tail", false))

	l := ll.jobs["j"]
	if !l.done || string(l.data) != "start tail" {
		t.Fatalf("got %q done=%v", l.data, l.done)
	}
}

func TestLiveLogs_FinishKeepsWhatArrived(t *testing.T) {
	ll := &liveLogs{jobs: map[string]*liveLog{}}
	ll.add(chunk(0, "a", false))
	ll.add(chunk(5, "b", false))
	ll.add(chunk(6, "", true))

	l := ll.jobs["j"]
	ll.finish(l)
	if got := string(l.data); !l.done || !strings.HasPrefix(got, "a\n") || !strings.HasSuffix(got, "b") {
		t.Fatalf("got %q done=%v", got, l.done)
	}
}

func TestLiveLogs_ChangedWakesWaiters(t *testing.T) {
	ll := &liveLogs{jobs: map[string]*liveLog{}}
	ll.get("j", true)
	changed := ll.jobs["j"].changed
	ll.add(chunk(0, "a", false))
	select {
	case <-changed:
	default:
		t.Fatal("waiters not woken")
	}
}
