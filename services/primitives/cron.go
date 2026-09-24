package primitives

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"google.golang.org/protobuf/proto"

	"github.com/taubyte/tau/pkg/invoke"
	primitivesv1 "github.com/taubyte/tau/pkg/proto/gen/tau/primitives/v1"
	"github.com/taubyte/tau/pkg/queue"
	"github.com/taubyte/tau/pkg/raft"
)

// cron.go is the clock, and the clock is TWO ACTS that are deliberately not the
// same node's work.
//
// # The leader's whole job is to say whose minute it is
//
// Once a minute the raft leader hands ONE minute to ONE node — "minute T is
// yours: generate and distribute the cron jobs" — and does nothing else. It
// reads no position, opens no queue and looks at no schedule, so its cost per
// minute is one tasking whether the cloud holds ten crons or ten million.
//
// THE LOOP THIS REPLACES COULD NOT SAY THAT. It read a `cron/past/<handle>` key
// and opened a queue to ask its depth FOR EVERY SCHEDULE, every ten seconds, on
// the one node that may write — and pkg/raft's Keys does not push the prefix
// down to the store, so every one of those reads walked the whole namespace. It
// was O(N) per tick on a single node, and no index could have rescued it
// because the scan was underneath the index.
//
// # The tasking IS the exactly-once
//
// Raft already made the leader singular. One leader handing one minute to one
// node is therefore the whole of "this minute happens once": there is no
// per-tick replicated record, nothing for the work to be atomic with, and
// nothing for a later turn to read back and disagree about.
//
// WHICH PUTS THE CLOCK'S MEMORY IN ONE PROCESS, and that is this version's
// trade, made deliberately. The minute a leader has already handed out is a
// FIELD and not a key — see [crons.tasked] — so a node that wins an election
// starts by recording where the clock is and tasking nothing. What a leadership
// change costs is at most one minute's slots, and never a second run of one.
// Losing a slot is the direction to fail in; the other one runs somebody's
// billing twice.
//
// # The tasking is an RPC and the node it reaches is somebody else's
//
// `CronTask(since, upto)` crosses a p2p stream to a node running this service —
// which may be this one and in a fleet usually is not. The leader picks it
// through the composer (see [WithTasking]) because WHICH NODE IS A ROUTING
// QUESTION: node/primitives.go already reads the fleet's view to find a
// primitives service, and a picker here would be a second discovery path
// disagreeing with the first.
//
// ANY PRIMITIVES NODE ACCEPTS IT AND ROUTES INTERNALLY TO WHOEVER CAN WRITE,
// which is services/database/forward.go's shape and is chosen over "the leader
// picks the node that leads": the picker would have to know another layer's
// internal leadership, and would be recomputing per minute a fact the group
// itself holds. So a tasked node that does not lead the namespace sends the
// window on to the node its own group names, ONCE — a tasking that has already
// been sent on is served where it lands or refused there, for forward.go's
// reason: two nodes whose views differ would otherwise pass it to each other.
//
// AND THE LAST HOP IS THE LEADER'S, BECAUSE A FIRE IS A WRITE. Pushing an item
// and claiming it back are both refused off the leader and NOT forwarded — that
// is pkg/raft's rule and pkg/queue's, stated at length in this package's doc —
// so "whoever can write" resolves to one node. The day pkg/raft forwards a
// write, or the clock's group stops being this service's own, nothing above
// this line changes: the picker, the RPC and the routing are already the ones
// that shape wants.
//
// A TASKING THAT LANDS WHERE IT CANNOT COMMIT FAILS LOUDLY AND IS RE-TASKED.
// The refusal carries the node that may write, the leader does not record the
// window as spent, and the next turn hands the same window out again widened by
// whatever has come round since. There is no task record, no lease and no retry
// queue: an unanswered tasking is one the clock has not moved past.
//
// # A FIRE IS AN ITEM AND NOT AN INVOCATION
//
// The tasked node pushes ONE ITEM PER THING OWED into a queue of the schedule's
// own and then claims it back, so a fire inherits the visibility timeout, the
// attempt budget, the backoff and the dead letter pkg/queue already implements.
// Before that a failed fire was a log line: a slot spent and work that silently
// never happened, with nothing anywhere to try it again.
//
// THE QUEUE IS A PLATFORM QUEUE AND NOT A DECLARED RESOURCE. A cron's target
// declares nothing — the vocabulary's own sentence is "it fires whatever it
// edges to; the target declares nothing" — so there is no declaration to
// resolve, and [Tier.Send] refuses an undeclared resource by name on purpose.
// It is opened directly on this service's group from the schedule's handle, and
// it is NOT written into the `opened/` index: the lifecycle pass walks that
// index and resolves every entry back to a declaration, and this one has none.
// Its sweep and its dead letter's bound ride the pass that fires it instead —
// see [crons.generate].
//
// WHAT THAT TRADES, SAID PLAINLY: the minute is owned exactly once and the fire
// is delivered AT LEAST once — deduplicated while the item is live, bounded by
// the attempt budget, and visible in a dead letter when it gives up. A workload
// fired by a clock has `X-Taubyte-Fired-At` as the key it can deduplicate a
// slot on.
//
// AND A REDELIVERY WAITS FOR THE SCHEDULE'S NEXT SLOT rather than for
// fireRetry, which is the one thing that got slower here. Touching a queue at
// all costs a barrier and a walk of the whole namespace, so the pass touches
// only the schedules that are DUE — the alternative is the per-schedule walk
// this replaced. The backoff, the attempt budget and the dead letter are
// unchanged and still bound the attempts; what changed is the spacing. For
// `* * * * *` that is a minute. For `0 3 * * *` it is a day, and a `skip`
// schedule with a fire outstanding does not start its next slot until that fire
// is acked or buried — so a daily cron whose target is down burns its budget
// one day at a time. Closing that needs a cheap way to find the queues that
// have something in them, which is an index, which is the thing this change
// exists to not need.

// Target is one thing a schedule edges to, as an address whatever fires it can
// invoke.
//
// IT IS THIS PACKAGE'S OWN AND NOT services/worker's. What a cron edges to is
// an address, and a consensus group that imported a compute service to say so
// would be the dependency pointing the wrong way. The composer that has both
// shapes open is the one place they meet — see [WithSchedules].
type Target struct {
	Project     string
	Environment string

	// Scope is where the target lives inside the project, as the segments the
	// compiled object nests it under.
	Scope []string

	Kind string
	Name string
}

// key is this target as one string, for the item id it takes part in. It is
// used for DEDUPLICATION ONLY and is never parsed back: a name is author-
// supplied text, so reading a target out of an id would be a separator bug
// waiting to fire the wrong one.
func (t Target) key() string {
	return strings.Join(append(append([]string{}, t.Scope...), t.Kind, t.Name), "/")
}

// Schedule is one cron as its author wrote it, already resolved: when it fires
// and what it fires.
type Schedule struct {
	// Handle is the deployment and the cron's own id, which is what names this
	// schedule's fire queue.
	Handle string

	// Name is the author's own name for it, which is what a fired workload is
	// told it is a run of — together with ID, the cron's own resource id, and
	// Path, where it lives, because a name alone is unique only inside its own
	// scope and does not survive a rename. See deliveryHeaders.
	Name string
	ID   string
	Path string

	// Spec is the cron expression and Timezone is the IANA zone it is written
	// in. A cron expression alone does not name a moment.
	Spec     string
	Timezone string

	// OnOverlap is what to do when the clock comes round and the last run is
	// still going: "skip", "queue" or "allow".
	OnOverlap string

	// Jitter spreads the start randomly across this window.
	Jitter time.Duration

	// Fires is every target this cron edges to.
	Fires []Target

	// Starts is every workflow it begins a run of, as the grant that says where
	// a start lands — which is the FIRST STEP'S QUEUE and not the workflow, so
	// the item lands where a consumer is actually draining.
	Starts []invoke.Grant

	// Refused, when set, is why this cloud will not fire some part of it — a
	// target that resolved to nothing, a workflow whose first step it cannot
	// name. It is the composer's sentence and NOTHING HERE READS IT: a refusal
	// has already removed the target it refused, so what decides whether a
	// schedule is worth opening a queue for is [Schedule.owes] and not this.
	// A cron whose every edge was refused owes nothing and is walked past.
	Refused string
}

// owes is whether this schedule has anything for a slot to do. A cron that
// edges to nothing this cloud serves is never opened at all.
func (s Schedule) owes() bool { return len(s.Fires)+len(s.Starts) > 0 }

// WithSchedules is where this service reads the clock's work from.
//
// IT IS INJECTED BECAUSE ENUMERATION IS NOT THIS SERVICE'S. Which crons exist
// is a property of the deployments a compute node is already serving — the
// register deliberately does not enumerate — so the composer that holds both a
// worker and this service is the one thing that can answer it. Without it this
// service keeps queues and logs and runs no clock at all.
func WithSchedules(of func(context.Context) []Schedule) Option {
	return func(o *options) error {
		if of == nil {
			return errors.New("primitives: a nil schedule source")
		}
		o.schedules = of
		return nil
	}
}

// WithFire is how a fire reaches the workload it names.
//
// THROUGH PLACEMENT, WHICH IS WHY IT IS INJECTED AND NOT DONE HERE. A claim is
// leader-only, so a service that also RAN what it claimed would put every fire
// in the cloud on one node with the rest of the fleet idle. The composer passes
// the same placement node/dispatch.go uses for a delivery, and that is the
// whole mechanism by which the work spreads.
func WithFire(run func(context.Context, Target, *invoke.Request) (*invoke.Response, error)) Option {
	return func(o *options) error {
		if run == nil {
			return errors.New("primitives: a nil fire")
		}
		o.fire = run
		return nil
	}
}

const (
	// firePrefix names a schedule's own fire queue. It is a segment neither
	// pkg/queue's keyspace nor the `opened/` index writes under, so nothing in
	// this namespace collides with it.
	firePrefix = "cron/fire/"

	// maxLateness is how far back a tasking may reach. Beyond it the gap is
	// skipped: a cloud that was down for a month should come back and do
	// today's work, not last month's.
	maxLateness = 24 * time.Hour

	// maxSlotsScanned bounds the walk that finds the most recent slot in a
	// window. A minutely schedule over maxLateness is 1440 steps, so this is
	// the window with room rather than a limit anything real reaches.
	maxSlotsScanned = 4096

	// fireVisibility is how long a claimed fire stays invisible, ON TOP OF the
	// schedule's own jitter. The jitter is waited out by the consumer AFTER the
	// claim, so a window that did not cover it would hand the same slot to
	// somebody else while the first holder was still waiting to start.
	fireVisibility = 2 * time.Minute

	// fireRetry spaces a redelivery after a fire that failed.
	//
	// WITHOUT ONE A FAILURE IS IMMEDIATE, which node/dispatch.go records as a
	// real defect: a consumer failing in a millisecond burns every attempt in a
	// millisecond and the work is in the dead letter before anybody could have
	// fixed anything.
	fireRetry = 30 * time.Second

	// fireRetryFactor and fireRetryCeiling back a failing target off rather
	// than hammering it at a fixed rate for its whole budget.
	fireRetryFactor  = 2
	fireRetryCeiling = 10 * time.Minute

	// taskTimeout bounds one tasking, end to end — the stream, the node that
	// answers, and the hop it may make.
	//
	// IT IS THE CLOCK'S OWN GRAIN. A window not generated within the minute it
	// names is one the next turn hands out again, widened, so waiting longer
	// buys nothing and costs the leader's single periodic goroutine. Close
	// cancels it sooner by cancelling the pass's context.
	taskTimeout = time.Minute
)

// The three values `on_overlap` takes, as the vocabulary spells them. `skip` is
// its default and is the only one that cannot make things worse.
const (
	overlapSkip  = "skip"
	overlapQueue = "queue"
	overlapAllow = "allow"
)

// deliveryHeaders for a fired schedule. A workload invoked by a clock has no
// request to read, so what it is told is WHICH CRON and which moment — and the
// MOMENT IS THE SLOT rather than now, so a run spread by jitter, a run that was
// late and a run that is a redelivery all say what they are a run of.
//
// WHICH CRON IS THREE HEADERS BECAUSE A NAME IS NOT AN IDENTITY. The name is
// what the author typed and is unique only inside its own scope — three
// applications can each hold a `nightly`, and a rename changes it. So the id
// goes with it, which is what the cron is called everywhere the author's name
// is not, and the path, which is where it lives: together they are the
// resource, and a workload fired by several crons can tell them apart, look
// its own up, and say in a log which one it is a run of.
const (
	headerCron     = "X-Taubyte-Cron"
	headerCronID   = "X-Taubyte-Cron-Id"
	headerCronPath = "X-Taubyte-Cron-Path"
	headerFiredAt  = "X-Taubyte-Fired-At"
)

// cronParser is the five-field form the vocabulary documents — "0 3 * * *" —
// with the descriptors people expect (@daily, @hourly) and no seconds field.
//
// NO SECONDS, deliberately: adding a field silently changes what every existing
// five-field expression means, and the vocabulary's doc and its fixture are
// both five.
var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// crons is the clock half of this service: the leader's tasking, and the pass a
// tasking runs.
type crons struct {
	service *Service

	// mu guards tasked, which the service's own loop writes and a test drives
	// from its own goroutine.
	mu sync.Mutex

	// tasked is the last minute this node has handed out, AND IT IS THIS
	// PROCESS'S MEMORY AND NOTHING ELSE — no key, no entry, nothing another
	// node can read. See the file's header for why that is the trade and which
	// way it fails.
	tasked time.Time
}

// tick is everything the leader does, and it is one tasking a minute.
//
// THE MINUTE IS THE GRAIN because a cron expression has no finer one. A turn
// that finds the clock still inside a minute it has already handed out does
// nothing at all: no read, no write, no walk.
func (c *crons) tick(ctx context.Context, now time.Time) {
	minute := now.UTC().Truncate(time.Minute)

	c.mu.Lock()
	since := c.tasked
	c.mu.Unlock()

	if since.IsZero() {
		// FIRST SIGHT: record where the clock is and task nothing. A nightly
		// job deployed at noon must not immediately run last night — and a node
		// that has just won an election must not re-run the minute its
		// predecessor was already handing out, which is the same sentence read
		// from the other side.
		c.advance(minute)
		return
	}
	if !minute.After(since) {
		return
	}

	// A MISSED MINUTE IS CAUGHT UP ONCE AND NOT REPLAYED. What travels is the
	// WINDOW `(since, minute]` rather than a minute on its own, so a cloud that
	// stalled from 03:00 to 03:55 owes the 03:00 job one run and not eleven —
	// the tasked node takes the most recent slot in the window and skips the
	// rest. maxLateness is enforced where the window is walked; see [slotAt].
	if err := c.assign(ctx, since, minute); err != nil {
		// NOT ADVANCED, so the next turn hands out the same window widened by
		// whatever has come round since. RE-TASKING IS THE CATCH-UP, which is
		// the whole of what "re-task if the node does not answer" needs.
		logger.Warnf("cron: nobody took the minutes after %s: %s", since.Format(time.RFC3339), err)
		return
	}
	c.advance(minute)
}

func (c *crons) advance(minute time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tasked = minute
}

// assign hands one window to a node that will generate and distribute the cron
// jobs it owes. It is ONE MESSAGE and the whole of the leader's work.
//
// THE PICK IS THE COMPOSER'S — see [WithTasking] — so a service with no view of
// a fleet tasks itself and one with a view may task anybody. Nothing about
// which node is decided here.
//
// BOUNDED, BECAUSE THE LEADER HAS ONE PERIODIC GOROUTINE. A tasking that hangs
// against a node on its way out would hold the lifecycle pass with it. The
// bound is the clock's own grain: a window not generated within the minute it
// names is one the next turn hands out again.
func (c *crons) assign(ctx context.Context, since, upto time.Time) error {
	tasking, stop := context.WithTimeout(ctx, taskTimeout)
	defer stop()
	return c.service.task(tasking, since, upto)
}

// accept is the other end: one window, on the node the leader chose.
//
// IT IS THE ONE DOOR, for local and remote alike. [Tier.CronTask] is this call
// without a stream and the handler in rpc.go is this call with one, so what a
// tasked node does is decided here rather than twice.
//
// THE BARRIER IS HERE AND NOT WHERE THE TASKING CAME FROM. A window that
// arrived over a stream cannot inherit the caller's read: everything below
// decides on what it reads — a fire queue's depth read stale is a schedule that
// reads as idle while its last run is still going, which is `skip` and `queue`
// both silently becoming `allow` — and a node that has just become leader can
// hold committed entries it has not applied.
func (c *crons) accept(ctx context.Context, since, upto time.Time) error {
	cluster, err := c.service.consensus()
	if err != nil {
		return err
	}
	if !cluster.IsLeader() {
		return c.forward(ctx, cluster, since, upto)
	}
	if c.service.schedules == nil {
		// A NODE THAT MAY WRITE AND HAS NOTHING TO WALK. It holds the namespace
		// and runs no compute, so there is no deployment view to enumerate
		// crons from — see WithSchedules. Saying so is what makes the window
		// re-taskable; generating nothing would spend it.
		return ErrNoClock
	}
	if err := cluster.Barrier(applyTimeout); err != nil {
		return err
	}
	return c.generate(ctx, since, upto)
}

// forward sends one window on to the node that may write it.
//
// ONCE, AND NEVER BACK. A tasking that has already been sent on is served where
// it lands or refused there — services/database/forward.go's rule, for its
// reason: two nodes whose views of the group differ would otherwise pass the
// window to each other for as long as they disagreed, and a refusal the leader
// can retry next minute is the cheaper answer.
func (c *crons) forward(ctx context.Context, cluster raft.Cluster, since, upto time.Time) error {
	if hops := hopsOf(ctx); hops >= maxHops {
		return fmt.Errorf("%w: and this tasking has already come %d nodes", ErrNotLeading, hops)
	}

	leader, err := cluster.Leader()
	if err != nil {
		// NOBODY LEADS THIS NAMESPACE YET. It is the same refusal a write gets,
		// unnamed, and the leader that handed the window out retries it.
		return NotLeading{}
	}

	if leader == c.service.host.ID() {
		// THIS NODE BETWEEN TWO ANSWERS. It was not the leader when it was
		// asked and its group names it now; refusing is honest — the next
		// minute asks again, by which time one of the two has settled — and
		// dialling itself would be a stream back to the door it is standing in.
		return NotLeading{Leader: leader.String()}
	}

	client, err := NewClient(c.service.host, leader)
	if err != nil {
		return fmt.Errorf("%w: reaching %s: %w", ErrNotLeading, leader, err)
	}
	defer client.Close()

	if err := client.CronTask(ctx, since, upto); err != nil {
		// THE NODE IT WENT TO IS NAMED AND ITS ANSWER IS KEPT IN THE CHAIN. A
		// refusal from the node that leads is a different fact from one that
		// never got there, and an operator reading the leader's log has only
		// this line to tell them apart.
		return fmt.Errorf("handing the window to %s, which leads: %w", leader, err)
	}
	return nil
}

// hopHeader carries how many nodes a tasking has already been sent through.
//
// A HEADER AND NOT A FIELD ON CronTaskRequest, for the reason
// services/database/forward.go gives: it is about the call's route and not
// about the window, and a caller could not usefully set it.
const hopHeader = "Tau-Primitives-Hops"

// maxHops is how many nodes a tasking may be sent through. Two is the shape the
// fleet has: the leader hands the window to a primitives node, and that node
// hands it to the one its group says may write. A third would be two nodes
// disagreeing, which is what the next minute is for.
const maxHops = 2

type hopsKey struct{}

// hopsIn reads how far a tasking has come. An unreadable or negative count is
// read as the ceiling rather than as zero: a header this node cannot understand
// must not be the thing that lets a window go round again.
func hopsIn(ctx context.Context, header http.Header) context.Context {
	raw := header.Get(hopHeader)
	if raw == "" {
		return ctx
	}
	hops, err := strconv.Atoi(raw)
	if err != nil || hops < 0 {
		hops = maxHops
	}
	return context.WithValue(ctx, hopsKey{}, hops)
}

// hopsOf is how far the tasking being served has come. Zero is the leader's own
// turn, whether it tasked itself or is about to task somebody.
func hopsOf(ctx context.Context) int {
	hops, _ := ctx.Value(hopsKey{}).(int)
	return hops
}

// generate is what a tasked node does with the window it was handed: work out
// which schedules it owes a run, push what they owe, and hand out what is
// ready.
//
// THE WALK IS THE TASKED NODE'S AND THAT IS THE POINT. It is O(the schedules
// this node holds) in cron expressions parsed — arithmetic, with no read and no
// write behind it — and O(the schedules that are DUE) in anything that touches
// the namespace. A schedule whose clock has not come round is not opened, not
// asked its depth and not swept.
//
// THE SWEEP AND THE DEAD LETTER RIDE HERE AND NOT IN retention.go, because a
// fire queue is not declared: the lifecycle pass walks the `opened/` index and
// resolves each entry back to a declaration, and a platform queue has none. So
// this is the only pass that reaches one, and without it an exhausted fire's
// dead letter would grow forever inside a state machine every voter holds in
// memory.
func (c *crons) generate(ctx context.Context, since, upto time.Time) error {
	var pushed int
	var refused error

	for _, schedule := range c.service.schedules(ctx) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !schedule.owes() {
			// A cron edging to nothing this cloud serves. Saying so once is
			// the composer's; it is not this pass's every minute.
			continue
		}
		slot, due := slotAt(schedule, since, upto)
		if !due {
			continue
		}

		held, err := c.open(schedule)
		if err != nil {
			logger.Warnf("cron: opening the fires of %s: %s", schedule.Name, err)
			continue
		}

		// THE SWEEP FIRST, BECAUSE THE TWO READS BELOW BELIEVE IT. It runs the
		// same pass Claim runs — an expired claim back to ready, an exhausted
		// one buried — so a consumer that died holding a fire does not leave
		// this schedule reading as busy forever, which is what `skip` and
		// `queue` would both act on.
		if err := held.Sweep(); err != nil && !errors.Is(err, queue.ErrNotLeader) {
			logger.Warnf("cron: sweeping the fires of %s: %s", schedule.Name, err)
		}

		if err := c.push(schedule, held, slot); err != nil {
			refused = err
			logger.Warnf("cron: %s owes %s and nothing took it: %s", schedule.Name, slot.Format(time.RFC3339), err)
		} else {
			pushed++
		}
		c.drain(schedule, held)
		c.bound(schedule, held)
	}

	// A NODE THAT COMMITTED NOTHING HAS NOT SPENT THE WINDOW. Every push being
	// refused is one condition and not many — this node stopped being the one
	// that may write, or its group is no longer there — so saying so hands the
	// window back to the next turn INTACT, with nothing already committed for a
	// re-tasking to duplicate.
	if pushed == 0 && refused != nil {
		return refused
	}
	return nil
}

// push is what one slot of one schedule owes, as items in its own fire queue.
//
// `skip` IS THE ONLY VALUE THAT READS ANYTHING FIRST, and what it reads is the
// queue's own depth: an item is settled only once its invocation has returned,
// so "the previous run is still going" is a fact this namespace holds rather
// than a flag in the memory of whichever node happened to start it.
//
// `queue` PUSHES ANYWAY AND THE QUEUE IS THE BACKLOG. The slot waits as an item
// behind the one in flight, which is what the value asks for — and the
// vocabulary's own warning about it, that it "turns a slow job into an
// unbounded backlog", is now literally what it does rather than an
// approximation of it kept one deep.
func (c *crons) push(schedule Schedule, held *queue.Queue, slot time.Time) error {
	items := owed(schedule, slot)
	if len(items) == 0 {
		return nil
	}
	if overlapOf(schedule) == overlapSkip {
		if ready, claimed := held.Depth(); ready+claimed > 0 {
			return nil
		}
	}
	return held.PushWith(items)
}

// drain hands out this schedule's fires: the ones the push above just made, and
// any earlier one a failed delivery put back whose backoff has elapsed.
//
// IT CLAIMS AND DOES NOT WAIT. An invocation is the author's code with the
// author's timeout on it, and a pass that ran one inline would make every other
// schedule in the cloud late by however long somebody's job takes. So the claim
// is the pass's and the invocation is a goroutine's, which is the same split
// [Service.Close] waits out.
//
// ONE SLOT AT A TIME UNLESS THE AUTHOR SAID OTHERWISE — one SLOT, and not one
// item. With two slots of one schedule in flight at once, `queue` and `skip`
// both degrade into `allow`, and the value the vocabulary names as the
// correctness case is the one that stopped being kept. A slot's own targets are
// not that: they are one run of the cron, and "it fires whatever it edges to"
// is a sentence about one moment.
//
// A FIRE RUNS ON THIS SERVICE'S LIFETIME AND NOT ON THE TASKING'S. The window
// arrives over a stream now, and a caller that gave up — or simply got its
// answer — would otherwise cancel every invocation the pass had just handed
// out. What bounds one fire is its own claim deadline; what ends them all is
// Close, which cancels that lifetime and then waits them out.
func (c *crons) drain(schedule Schedule, held *queue.Queue) {
	ready, claimed := held.Depth()

	take := len(schedule.Fires) + len(schedule.Starts)
	if overlapOf(schedule) == overlapAllow {
		take = ready
	} else if claimed > 0 {
		// A slot of this schedule is already out with somebody.
		return
	}

	running := c.service.lifetime()
	for range take {
		item, err := held.Claim()
		if errors.Is(err, queue.ErrNoItem) {
			return
		}
		if err != nil {
			if !errors.Is(err, queue.ErrNotLeader) {
				logger.Warnf("cron: claiming a fire of %s: %s", schedule.Name, err)
			}
			return
		}
		c.service.deliveries.Add(1)
		go func() {
			defer c.service.deliveries.Done()
			c.run(running, held, schedule, item)
		}()
	}
}

// bound is what a fire queue's dead letter keeps.
//
// IT IS HERE BECAUSE NOTHING ELSE REACHES ONE. A dead letter is derived from
// its queue's name, so it is not a declared resource, no consumer can ever be
// declared for it, and every item in it sits inline in a state machine every
// voter holds in memory. See DefaultDeadLetterRetention.
func (c *crons) bound(schedule Schedule, held *queue.Queue) {
	dead, err := held.Dead()
	if err != nil {
		return
	}
	dropped, err := dead.Expire(c.service.dead.age, c.service.dead.size)
	if err != nil && !errors.Is(err, queue.ErrNotLeader) {
		logger.Warnf("cron: expiring the dead fires of %s: %s", schedule.Name, err)
		return
	}
	if dropped > 0 {
		logger.Debugf("primitives: %s dropped %d dead-lettered fires", schedule.Name, dropped)
	}
}

// overlapOf is what this schedule says to do about the run before it, with the
// vocabulary's own default filled in — so an object that came from anywhere but
// tcc is read the same way tcc would have written it.
func overlapOf(schedule Schedule) string {
	switch schedule.OnOverlap {
	case overlapQueue, overlapAllow:
		return schedule.OnOverlap
	default:
		return overlapSkip
	}
}

// owed is everything one slot of one schedule owes, as the items that carry it.
//
// ONE ITEM PER THING OWED AND NOT ONE PER SLOT. One item for N targets means
// one failure nacks all N and the redelivery re-fires the ones that already
// succeeded; per-target items give per-target dedup, per-target retry and a
// per-target place in the dead letter.
//
// A START IS AN ITEM HERE TOO. It used to be sent straight out of the turn that
// spent the slot, with no queue of its own to retry it: a send refused by a
// group still forming spent the period and started nothing. As an item the send
// becomes the delivery's act, with the attempt budget every fire has.
//
// A TARGET NOTHING CAN ENCODE IS DROPPED AND NOT RETRIED. It is the same bytes
// on every turn, so a slot held open for it would be held open forever.
func owed(schedule Schedule, slot time.Time) []queue.Item {
	items := make([]queue.Item, 0, len(schedule.Fires)+len(schedule.Starts))

	add := func(id string, fire *primitivesv1.CronFire, what string) {
		fire.Cron, fire.Slot, fire.Jitter = schedule.Name, slot.Unix(), int64(schedule.Jitter)
		body, err := proto.Marshal(fire)
		if err != nil {
			logger.Warnf("cron: %s owes %s, which nothing can encode: %s", schedule.Name, what, err)
			return
		}
		items = append(items, queue.Item{ID: id, Data: body})
	}

	for _, target := range schedule.Fires {
		add(fireID(schedule, slot, target.key()), &primitivesv1.CronFire{
			Owes: &primitivesv1.CronFire_Target{Target: &primitivesv1.CronTarget{
				Project:     target.Project,
				Environment: target.Environment,
				Scope:       target.Scope,
				Kind:        target.Kind,
				Name:        target.Name,
			}},
		}, target.Kind+"/"+target.Name)
	}

	for _, start := range schedule.Starts {
		// THE HANDLE IS WHAT TRAVELS AND THE GRANT IS REBUILT. A grant is the
		// caller's side of a conversation — ops, settings, a label — and what a
		// start actually needs is the queue it lands in, which the handle names
		// on its own. See invoke.StartGrant for why the handle is the first
		// step's queue and never the workflow's own id.
		add(startID(schedule, slot, start.Handle), &primitivesv1.CronFire{
			Owes: &primitivesv1.CronFire_Start{Start: &primitivesv1.CronStart{
				Handle: start.Handle,
				Label:  start.Label,
			}},
		}, "a run of "+start.Label)
	}

	return items
}

// run is one fire: wait out the jitter, invoke, and settle on what came back.
//
// 2xx IS AN ACKNOWLEDGEMENT and everything else is a failure, which is the
// contract every push consumer in this market already speaks. A failure is
// NACKED rather than left to expire, because this queue declares a retry
// interval to space the next attempt by — see fireRetry.
func (c *crons) run(ctx context.Context, held *queue.Queue, schedule Schedule, item queue.Delivery) {
	fire := &primitivesv1.CronFire{}
	if err := proto.Unmarshal(item.Data, fire); err != nil || fire.GetOwes() == nil {
		// A BODY NOTHING CAN READ IS NOT WORK TO RETRY. Five more attempts at
		// the same bytes end in a dead letter nobody drains; discarding it says
		// so once.
		logger.Warnf("cron: %s fired an item nothing can read: %s", schedule.Name, item.ID)
		c.settle(held, schedule, item, true)
		return
	}

	// THE CLAIM'S DEADLINE BOUNDS THE FIRE. Past it the item is deliverable
	// again, so a workload still running is one whose work somebody else has
	// already been handed — and the ack would be refused anyway.
	//
	// AS A DURATION AND NOT AS AN INSTANT, which is not a style choice. The
	// deadline is stamped on the QUEUE's clock, and this service's clock is
	// injectable — a test drives it and a dream universe drives it — so
	// context.WithDeadline, which compares against the wall clock, cancels
	// every fire before it starts wherever the two read differently. What the
	// invocation is owed is the time left on the claim, measured on the clock
	// the claim was stamped by.
	budget := item.Deadline.Sub(c.service.clock())
	if budget <= 0 {
		// The claim expired between handing it out and getting here. Somebody
		// else has it or is about to; giving it back now is the honest answer.
		c.settle(held, schedule, item, false)
		return
	}
	runCtx, stop := context.WithTimeout(ctx, budget)
	defer stop()

	// JITTER IS WAITED OUT AFTER THE CLAIM, not before it. A producer that
	// slept first would leave the slot unclaimed for the length of the window,
	// which is exactly long enough for another node to become leader and hand
	// the same slot out too. fireVisibility covers this wait — see its doc.
	if jitter := time.Duration(fire.GetJitter()); jitter > 0 {
		select {
		case <-runCtx.Done():
			c.settle(held, schedule, item, false)
			return
		case <-time.After(time.Duration(rand.Int64N(int64(jitter)))):
		}
	}

	if start := fire.GetStart(); start != nil {
		c.settle(held, schedule, item, c.begin(runCtx, fire, start))
		return
	}
	c.settle(held, schedule, item, c.invoke(runCtx, schedule, fire))
}

// invoke is a fire that names a workload, and answers whether it was done.
//
// THE IDENTITY IS READ OFF THE SCHEDULE AND NOT THE ITEM. The item carries the
// name it was written with; the schedule is the cron as it stands this minute.
// They differ only across a rename, and there the live one is what the callee
// can look up — the handle naming the queue is the id either way.
func (c *crons) invoke(ctx context.Context, schedule Schedule, fire *primitivesv1.CronFire) bool {
	at := Target{
		Project:     fire.GetTarget().GetProject(),
		Environment: fire.GetTarget().GetEnvironment(),
		Scope:       fire.GetTarget().GetScope(),
		Kind:        fire.GetTarget().GetKind(),
		Name:        fire.GetTarget().GetName(),
	}
	slot := time.Unix(fire.GetSlot(), 0).UTC()

	res, err := c.service.fire(ctx, at, &invoke.Request{
		Method: http.MethodPost,
		Path:   "/",
		Headers: map[string][]string{
			headerCron:     {schedule.Name},
			headerCronID:   {schedule.ID},
			headerCronPath: {schedule.Path},
			headerFiredAt:  {slot.Format(time.RFC3339)},
		},
	})
	switch {
	case err != nil:
		logger.Warnf("cron: %s firing %s/%s: %s", fire.GetCron(), at.Kind, at.Name, err)
		return false
	case res.Status < 200 || res.Status > 299:
		logger.Warnf("cron: %s firing %s/%s answered %d", fire.GetCron(), at.Kind, at.Name, res.Status)
		return false
	}
	return true
}

// begin is a fire that starts a workflow run, and answers whether it landed.
//
// A RUN BEGINS AS AN ITEM IN ITS FIRST STEP'S DECLARED QUEUE, with the author's
// own budget on it — which is why this is a send and not an invocation: a
// workflow runs on no tier. The id is the cron and the SLOT, so a fire this
// queue delivered twice is still one run.
func (c *crons) begin(ctx context.Context, fire *primitivesv1.CronFire, start *primitivesv1.CronStart) bool {
	// THE HANDLE IS THE WHOLE OF WHAT IS READ. [Tier.Send] resolves a grant by
	// taking its handle apart and reading the DECLARATION — "the caller names a
	// primitive; it does not describe one" — so the author's budget comes off
	// the queue's own record and nothing here can describe a different one.
	// What the rest of the grant is for is the sentence a refusal prints.
	grant := invoke.Grant{
		Kind:   invoke.SyncKind,
		Label:  start.GetLabel(),
		Handle: start.GetHandle(),
		Ops:    []string{invoke.OpSend},
	}
	slot := time.Unix(fire.GetSlot(), 0).UTC()

	if err := c.service.Local().Send(ctx, grant, startRunID(fire.GetCron(), slot), nil); err != nil {
		logger.Warnf("cron: %s starting %s: %s", fire.GetCron(), start.GetLabel(), err)
		return false
	}
	return true
}

// settle ends one fire.
//
// A REFUSED SETTLE IS WORTH A LINE. What follows one is a SECOND fire of work
// that has already been done, so somebody looking at a duplicate has the one
// line that says why.
func (c *crons) settle(held *queue.Queue, schedule Schedule, item queue.Delivery, done bool) {
	var err error
	if done {
		err = held.Ack(item)
	} else {
		err = held.Nack(item)
	}
	if err != nil && !errors.Is(err, queue.ErrNotLeader) {
		logger.Warnf("cron: settling a fire of %s: %s", schedule.Name, err)
	}
}

// open is this schedule's own fire queue.
//
// OPENED PER CALL AND NEVER CACHED, which is pkg/queue's own rule: a Queue
// holds no state, and a cache here would pin the jitter and the budget from
// before a redeploy.
func (c *crons) open(schedule Schedule) (*queue.Queue, error) {
	group, err := c.service.work.ready()
	if err != nil {
		return nil, err
	}
	return group.Open(fireConfig(schedule))
}

// fireConfig is the budget a fire runs under.
//
// IT IS THE PLATFORM'S AND NOT THE AUTHOR'S, and that is a gap rather than a
// design: the vocabulary has the words for every field below, under `queue/`,
// and a cron has nowhere to write them because its target declares nothing.
// Until it does, these are what every fire in every cloud gets.
func fireConfig(schedule Schedule) queue.Config {
	return queue.Config{
		Name:              firePrefix + schedule.Handle,
		VisibilityTimeout: fireVisibility + schedule.Jitter,
		RetryInterval:     fireRetry,
		RetryFactor:       fireRetryFactor,
		RetryCeiling:      fireRetryCeiling,
		ApplyTimeout:      applyTimeout,
	}
}

// fireID is what makes a slot pushed twice one item.
//
// IT IS FOR DEDUPLICATION AND IS NEVER PARSED. pkg/queue's Push is a no-op for
// an id it already holds, so a window handed out twice pushes nothing new while
// the first item is still live. An id that was ACKED is gone, so this dedups
// within an item's lifetime and the tasking stays the thing that owns a minute.
func fireID(schedule Schedule, slot time.Time, what string) string {
	return schedule.Handle + "@" + strconv.FormatInt(slot.Unix(), 10) + "#" + what
}

// startID is the id a fire that begins a run carries, and startRunID is the id
// the run itself is sent under.
//
// THEY ARE TWO IDS FOR TWO QUEUES. The first deduplicates the FIRE inside this
// schedule's own queue, and carries the handle so a cron starting two workflows
// in one slot is two items. The second deduplicates the RUN inside the
// workflow's first step's queue, and is the author's name for the cron and the
// slot — so a fire delivered twice is one run rather than two.
func startID(schedule Schedule, slot time.Time, handle string) string {
	return fireID(schedule, slot, "start/"+handle)
}

func startRunID(cron string, slot time.Time) string {
	return cron + "@" + strconv.FormatInt(slot.Unix(), 10)
}

// slotAt is the most recent scheduled moment inside the window a tasking hands
// over — after `since` and at or before `upto` — in the zone the schedule was
// written in, or nothing when the clock has not come round inside it.
//
// IT WALKS FORWARD BECAUSE A CRON LIBRARY ANSWERS "WHEN NEXT" AND NOT "WHEN
// LAST". Starting from where the window opens means a gap is caught up to its
// most recent slot in ONE step rather than replayed, and a schedule that did
// not come round returns nothing at all.
//
// A WINDOW LONGER THAN maxLateness IS NOT MADE UP, and it is not walked either
// — the floor is what keeps a cloud that was down for a month from stepping
// through a month of minutes to find out.
func slotAt(schedule Schedule, since, upto time.Time) (time.Time, bool) {
	zone := schedule.Timezone
	if zone == "" {
		// The vocabulary defaults it, so an empty one is a compiled object that
		// did not come from tcc. UTC is the same answer the default is.
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

	from := upto.Add(-maxLateness)
	if since.After(from) {
		from = since
	}

	local := upto.In(location)
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
