package service

import (
	"fmt"
	"strings"
	"testing"
	"time"

	servicesCommon "github.com/taubyte/tau/services/common"
)

func TestLiveLogs_RetryStartsANewLog(t *testing.T) {
	ll := &liveLogs{jobs: map[string]*liveLog{}}
	ll.add(chunk(0, "first", false))
	ll.add(chunk(5, "", true))
	old := ll.jobs["j"]

	ll.add(chunk(0, "second", false))

	if got := string(ll.jobs["j"].data); got != "second" {
		t.Fatalf("retry's log: %q", got)
	}
	if string(old.data) != "first" {
		t.Fatalf("a stream's old log changed: %q", old.data)
	}
	if ll.total != len("second") {
		t.Fatalf("total %d", ll.total)
	}
}

func TestLiveLogs_DoneIgnoresLateChunks(t *testing.T) {
	ll := &liveLogs{jobs: map[string]*liveLog{}}
	ll.add(chunk(0, "a", false))
	ll.add(chunk(1, "", true))
	ll.add(chunk(1, "b", false))
	if got := string(ll.jobs["j"].data); got != "a" {
		t.Fatalf("got %q", got)
	}
}

func TestLiveLogs_Limits(t *testing.T) {
	t.Run("oversized chunk", func(t *testing.T) {
		ll := &liveLogs{jobs: map[string]*liveLog{}}
		ll.add(chunk(0, strings.Repeat("x", liveLogsMaxChunk+1), false))
		if len(ll.jobs) != 0 {
			t.Fatal("oversized chunk accepted")
		}
	})

	t.Run("number of jobs", func(t *testing.T) {
		ll := &liveLogs{jobs: map[string]*liveLog{}}
		for i := 0; i < liveLogsMaxJobs+10; i++ {
			ll.add(&servicesCommon.LiveLogChunk{Job: fmt.Sprint(i), Data: []byte("x")})
		}
		if len(ll.jobs) != liveLogsMaxJobs {
			t.Fatalf("%d jobs", len(ll.jobs))
		}
		if ll.watch("new", true) != nil {
			t.Fatal("watch went over the limit")
		}
	})

	t.Run("total size", func(t *testing.T) {
		old := liveLogsMaxTotal
		liveLogsMaxTotal = 10
		defer func() { liveLogsMaxTotal = old }()

		ll := &liveLogs{jobs: map[string]*liveLog{}}
		ll.add(chunk(0, "12345678", false))
		ll.add(chunk(8, "more than fits", false))
		ll.add(chunk(22, "more", false))

		got := string(ll.jobs["j"].data)
		if !strings.HasPrefix(got, "12345678\n") || strings.Count(got, "over the live log limit") != 1 {
			t.Fatalf("got %q", got)
		}
	})
}

func TestLiveLogs_GC(t *testing.T) {
	ll := &liveLogs{jobs: map[string]*liveLog{}}
	ll.add(chunk(0, "abc", false))
	ll.add(chunk(3, "", true))
	l := ll.jobs["j"]
	l.updated = time.Now().Add(-2 * liveLogsKeepIdle)

	ll.watch("j", false)
	ll.gc()
	if ll.jobs["j"] == nil {
		t.Fatal("collected a log being followed")
	}

	ll.unwatch(l)
	ll.gc()
	if ll.jobs["j"] != nil || ll.total != 0 {
		t.Fatalf("not collected: total=%d", ll.total)
	}
}
