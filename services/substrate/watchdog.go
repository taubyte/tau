package substrate

import (
	"context"
	"os"
	"sync/atomic"
	"time"

	httpCommon "github.com/taubyte/tau/services/substrate/components/http/common"
)

var (
	watchdogInterval = 30 * time.Second
	watchdogDeadline = 3 * time.Minute
	watchdogMisses   = 5
	watchdogExit     = func() { os.Exit(1) }
)

// startWatchdog restarts a node whose request path hangs while the process stays
// alive, so systemd's Restart=always can heal it. Only a probe that has not
// returned within the deadline is a miss: an error return (TNS busy, peers down,
// overload) is a live node and never counts. Each check that finds the same probe
// still out is one miss, so one hung probe exits the node after the deadline plus
// an interval per further miss: 5 minutes, past the longest a lookup can take.
func startWatchdog(ctx context.Context, probe func()) {
	interval, deadline, limit, exit := watchdogInterval, watchdogDeadline, watchdogMisses, watchdogExit
	go func() {
		var (
			running atomic.Bool
			started time.Time
			misses  int
		)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}

			if !running.Load() {
				misses = 0 // the last probe returned, whatever it returned
				started = time.Now()
				running.Store(true)
				go func() {
					defer running.Store(false)
					probe()
				}()
				continue
			}
			if time.Since(started) < deadline {
				continue
			}

			misses++
			logger.Errorf("watchdog: request path has not answered for %s (%d/%d)", time.Since(started).Round(time.Second), misses, limit)
			if misses >= limit {
				logger.Errorf("watchdog: a probe is still out after %d checks, the node is deadlocked; exiting so it is restarted", misses)
				exit()
				return
			}
		}
	}()
}

// probeLookup runs a lookup through the code real HTTP requests use. The host
// does not exist, so the expected answer is a not-found error; only a hang matters.
func (s *Service) probeLookup() {
	_, _ = s.components.http.Lookup(httpCommon.New("watchdog.invalid", "/", "GET"))
}

var (
	// Lookup is bounded by TNS Sends of 10s each (no retry loop): 2 resource
	// types x (index + candidates) x 10s, twice when the cache entry is
	// replaced, so ~80-120s with one or two candidates, plus a DNS check.
	// The bound sits well above that.
	stuckAfter = 5 * time.Minute
	stuckMin   = 20
	stuckTicks = 3
)

// startStuckWatchdog covers a hang on a single key, which the probe cannot see:
// many real requests sitting in the lookup step for far longer than it can take.
// Requests that fail or are refused quickly leave the step and never count.
func startStuckWatchdog(ctx context.Context, stuck func(time.Duration) int) {
	interval, after, min, ticks, exit := watchdogInterval, stuckAfter, stuckMin, stuckTicks, watchdogExit
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for run := 0; ; {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			n := stuck(after)
			if n < min {
				run = 0
				continue
			}
			run++
			logger.Errorf("watchdog: %d requests have been in lookup for over %s (%d/%d)", n, after, run, ticks)
			if run >= ticks {
				logger.Errorf("watchdog: requests stuck in lookup for %d checks in a row, the node is deadlocked; exiting so it is restarted", run)
				exit()
				return
			}
		}
	}()
}
