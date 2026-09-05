package node

import (
	"context"
	"errors"
	"math/rand/v2"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/raft"
	"github.com/taubyte/tau/services/worker"
)

// cron.go fires a schedule.
//
// # Exactly once across a fleet, and no election decides it
//
// Every node holding a deployment reads the same `0 3 * * *` and would fire at
// 03:00:00 exactly — once per node, for a job the author wrote once. What makes
// it once is the same thing that makes a queue claim exclusive: a raft write.
// The leader records WHICH SLOT it has fired before invoking anything, and a
// write off the leader is refused, so exactly one node gets there.
//
// THE RECORD IS REPLICATED AND NOT IN MEMORY, which is the whole point. A
// leader that kept the last slot in its own process would fire the 03:00 run
// again the moment leadership moved or the process restarted — and the
// vocabulary's own example of `on_overlap: queue` is "a billing run where every
// period must happen exactly once".
//
// # A missed tick is caught up once, and only once
//
// A cloud down from 02:00 to 04:00 owes the 03:00 job ONE run, which happens
// when it comes back — not zero, and not one per missed hour. What is recorded
// is how far the schedule has been ADVANCED PAST, so the catch-up is the most
// recent slot in the gap and the rest of the gap is skipped. A backlog of
// missed runs is a decision an author has to be able to state, and this
// vocabulary has no field for it.
//
// A GAP LONGER THAN maxLateness IS NOT MADE UP AT ALL. A cloud that was down
// for a month should come back and do today's work, not last month's.
//
// # A schedule seen for the first time does not fire its past
//
// The first tick that sees a cron records where the clock is and fires nothing.
// Otherwise deploying a nightly job at noon would immediately run last night —
// which is the surprise every scheduler people have used already avoids, and
// the one thing a new deployment must not do.

// crons fires the schedules of the deployments this node serves.
type crons struct {
	node *Node

	// running is what is in flight, by handle, so `on_overlap` means something.
	// It is per node and that is enough: only the leader fires, so only one
	// node has anything in flight.
	mu      sync.Mutex
	running map[string]bool
	queued  map[string]bool
}

const (
	// pastPrefix is where a schedule's position is recorded — the moment it has
	// been advanced past — in the same raft namespace a project's queues live
	// in. It is the same coordinated state about the same deployment, and a
	// second cluster would be a second consensus group and a second election
	// for it.
	pastPrefix = "cron/past/"

	// tickEvery is how often a node looks at the clock.
	//
	// A CRON EXPRESSION'S FINEST GRAIN IS A MINUTE, so anything under that is
	// about how promptly the minute is noticed rather than about resolution.
	// Ten seconds bounds the lateness of a fire and costs one map read per
	// schedule.
	tickEvery = 10 * time.Second

	// applyTimeout bounds the raft write that claims a slot.
	applyTimeout = 5 * time.Second

	// maxLateness is how far behind a schedule may be and still be caught up.
	// Beyond it the gap is skipped: a cloud that was down for a month should
	// come back and do today's work.
	maxLateness = 24 * time.Hour

	// maxSlotsScanned bounds the walk that finds the most recent slot in a gap.
	// A minutely schedule over maxLateness is 1440 steps, so this is the window
	// with room rather than a limit anything real reaches.
	maxSlotsScanned = 4096
)

// cronParser is the five-field form the vocabulary documents — "0 3 * * *" — with
// the descriptors people expect (@daily, @hourly) and no seconds field.
//
// NO SECONDS, deliberately: adding a field silently changes what every existing
// five-field expression means, and the vocabulary's doc and its fixture are
// both five.
var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// step performs exactly ONE cycle and reports how many schedules it fired.
//
// The split from loop is services/build/backend's and node/dispatch.go's, for
// the same reason: every branch that decides whether somebody's job runs has to
// be reachable from a test without a goroutine, a sleep or a real clock.
func (c *crons) step(ctx context.Context, now time.Time) int {
	if c.node.worker == nil || c.node.work == nil {
		return 0
	}

	cluster, err := c.node.work.consensus()
	if err != nil {
		return 0
	}

	// A FOLLOWER FIRES NOTHING and this is not papered over: the claim below is
	// a raft write that would be refused anyway, and evaluating schedules to
	// reach a guaranteed error is work done for nothing.
	if !cluster.IsLeader() {
		return 0
	}

	var fired int
	for _, schedule := range c.node.worker.Schedules(ctx) {
		if schedule.Refused != "" {
			// The cron edges to something this cloud does not run. Firing the
			// rest of its targets is still right; saying so once is not this
			// loop's job every ten seconds.
			if len(schedule.Fires) == 0 {
				continue
			}
		}
		if c.fire(ctx, cluster, schedule, now) {
			fired++
		}
	}
	return fired
}

// fire claims this schedule's current slot and invokes what it edges to.
func (c *crons) fire(ctx context.Context, cluster raft.Cluster, schedule worker.Schedule, now time.Time) bool {
	key := pastPrefix + schedule.Handle

	past, seen := position(cluster, key)
	if !seen {
		// FIRST SIGHT: record where the clock is and fire nothing. A nightly
		// job deployed at noon must not immediately run last night.
		record(cluster, key, now, schedule.Name)
		return false
	}

	slot, ok := slotAt(schedule, past, now)
	if !ok {
		return false
	}

	// THE CLAIM IS THE WRITE, and it happens BEFORE anything is invoked. A
	// record written afterwards would be a job that ran and then lost the note
	// saying so, which is the same job again on the next tick.
	if !record(cluster, key, slot, schedule.Name) {
		return false
	}

	if !c.begin(schedule) {
		// The tick was dropped by `on_overlap`, and it is still recorded —
		// that is what "skip this tick" means, and a slot left unrecorded
		// would be tried again ten seconds later against the same run.
		return false
	}

	go c.run(ctx, schedule, slot)
	return true
}

// position is how far this schedule has been advanced past, and whether it has
// ever been seen.
func position(cluster raft.Cluster, key string) (time.Time, bool) {
	recorded, held := cluster.Get(key)
	if !held {
		return time.Time{}, false
	}
	at, err := strconv.ParseInt(string(recorded), 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(at, 0).UTC(), true
}

// record advances the schedule's position. It is leader-only, which is what
// makes exactly one node in a fleet get past it.
func record(cluster raft.Cluster, key string, at time.Time, name string) bool {
	err := cluster.Set(key, []byte(strconv.FormatInt(at.Unix(), 10)), applyTimeout)
	if err != nil && !errors.Is(err, raft.ErrNotLeader) {
		logger.Warnf("cron: claiming %s: %s", name, err)
	}
	return err == nil
}

// begin applies `on_overlap` and reports whether this tick runs now.
func (c *crons) begin(schedule worker.Schedule) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.running == nil {
		c.running, c.queued = map[string]bool{}, map[string]bool{}
	}
	if !c.running[schedule.Handle] {
		c.running[schedule.Handle] = true
		return true
	}

	switch schedule.OnOverlap {
	case "allow":
		// Alongside. Nothing to track: a second run is a second goroutine and
		// the flag is already set.
		return true
	case "queue":
		// Held until the one in flight ends. ONE DEEP AND NOT A BACKLOG: the
		// vocabulary's own warning about this value is that it "turns a slow
		// job into an unbounded backlog", and a queue of one says "the next
		// tick still owes a run" without becoming that.
		c.queued[schedule.Handle] = true
		return false
	default: // skip, and the vocabulary's default
		return false
	}
}

// end releases the schedule and runs the tick that was held, if there was one.
func (c *crons) end(schedule worker.Schedule) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.queued[schedule.Handle] {
		delete(c.queued, schedule.Handle)
		return true
	}
	delete(c.running, schedule.Handle)
	return false
}

// run invokes every target, spread across the jitter window.
func (c *crons) run(ctx context.Context, schedule worker.Schedule, slot time.Time) {
	for {
		// JITTER IS WAITED OUT AFTER THE CLAIM, not before it. A node that
		// slept first would leave the slot unclaimed for the length of the
		// window, which is exactly long enough for another node to become
		// leader and claim it too.
		if schedule.Jitter > 0 {
			select {
			case <-ctx.Done():
				c.end(schedule)
				return
			case <-time.After(time.Duration(rand.Int64N(int64(schedule.Jitter)))):
			}
		}

		for _, at := range schedule.Fires {
			res, err := c.node.worker.Invoke(ctx, at, &invoke.Request{
				Method: http.MethodPost,
				Path:   "/",
				Headers: map[string][]string{
					headerCron:    {schedule.Name},
					headerFiredAt: {slot.UTC().Format(time.RFC3339)},
				},
			})
			switch {
			case err != nil:
				logger.Warnf("cron: %s firing %s/%s: %s", schedule.Name, at.Kind, at.Name, err)
			case res.Status < 200 || res.Status > 299:
				logger.Warnf("cron: %s firing %s/%s answered %d", schedule.Name, at.Kind, at.Name, res.Status)
			}
		}

		// A TICK HELD BY `on_overlap: queue` RUNS HERE, in this goroutine,
		// which is what keeps "queue" from being "allow with extra steps".
		if !c.end(schedule) {
			return
		}
	}
}

// deliveryHeaders for a fired schedule. A workload invoked by a clock has no
// request to read, so what it is told is which cron and which moment — and the
// MOMENT IS THE SLOT rather than now, so a run spread by jitter and a run that
// was late both say what they are a run of.
const (
	headerCron    = "X-Taubyte-Cron"
	headerFiredAt = "X-Taubyte-Fired-At"
)

// slotAt is the most recent scheduled moment after `past` and at or before now,
// in the zone the schedule was written in — or nothing, when the clock has not
// come round since.
//
// IT WALKS FORWARD BECAUSE A CRON LIBRARY ANSWERS "WHEN NEXT" AND NOT "WHEN
// LAST". Starting from where the schedule was left means a gap is caught up to
// its most recent slot in one step rather than replayed, and a schedule that
// has not come round returns nothing at all.
func slotAt(schedule worker.Schedule, past, now time.Time) (time.Time, bool) {
	zone := schedule.Timezone
	if zone == "" {
		// The vocabulary defaults it, so an empty one is a compiled object
		// that did not come from tcc. UTC is the same answer the default is.
		zone = "UTC"
	}
	location, err := time.LoadLocation(zone)
	if err != nil {
		logger.Warnf("cron: %s names the timezone %q, which this machine does not have", schedule.Name, zone)
		return time.Time{}, false
	}

	parsed, err := cronParser.Parse(schedule.Spec)
	if err != nil {
		logger.Warnf("cron: %s has the schedule %q, which is not a cron expression: %s", schedule.Name, schedule.Spec, err)
		return time.Time{}, false
	}

	// A GAP LONGER THAN maxLateness IS NOT REPLAYED, and it is not walked
	// either — the floor is what keeps a cloud that was down for a month from
	// stepping through a month of minutes to find out.
	from := now.Add(-maxLateness)
	if past.After(from) {
		from = past
	}

	local := now.In(location)
	cursor := from.In(location)

	var last time.Time
	var found bool
	for range maxSlotsScanned {
		next := parsed.Next(cursor)
		if next.After(local) {
			break
		}
		last, found, cursor = next, true, next
	}
	return last, found
}

// loop cycles step until the node closes.
func (c *crons) loop(ctx context.Context, done chan<- struct{}) {
	defer close(done)

	tick := time.NewTicker(tickEvery)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick.C:
			c.step(ctx, now)
		}
	}
}
