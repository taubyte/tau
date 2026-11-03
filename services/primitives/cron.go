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

	"github.com/taubyte/tau/pkg/specs"

	"github.com/robfig/cron/v3"
	"google.golang.org/protobuf/proto"

	"github.com/taubyte/tau/pkg/invoke"
	primitivesv2 "github.com/taubyte/tau/pkg/proto/gen/tau/primitives/v2"
	"github.com/taubyte/tau/pkg/queue"
	"github.com/taubyte/tau/pkg/raft"
)

// The clock is two acts on different nodes. Once a minute the raft leader hands one
// window to one node and does nothing else, so its cost is one tasking per minute
// regardless of how many crons exist. Raft makes the leader singular, so the tasking
// is the exactly-once. The minute already handed out is a field, not a key: a new
// leader records where the clock is and tasks nothing, so a leadership change can
// lose at most one minute's slots and never run one twice.
//
// The tasking is an RPC to a node the composer picks (WithTasking). Any primitives
// node accepts it and forwards once to the node its group says may write, since a
// fire is a write and pkg/raft refuses writes off the leader. A tasking that cannot
// commit is refused, not recorded, and the next turn hands out the same window
// widened.
//
// A fire is an item in a platform queue of the schedule's own, then claimed back, so
// it inherits pkg/queue's visibility timeout, attempt budget, backoff and dead
// letter. The queue is not declared and not in the `opened/` index; generate sweeps
// and bounds it. The minute is owned exactly once; the fire is delivered at least
// once, and X-Taubyte-Fired-At is the slot to deduplicate on. Only due schedules are
// touched, so a redelivery waits for the schedule's next slot.

// Target is one thing a schedule edges to, as an address. This package's own type,
// so a consensus group does not import a compute service; the composer converts (see
// WithSchedules).
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

	// Name is the author's name for it; with ID (the resource id) and Path (where it
	// lives) it identifies the cron to a fired workload. See deliveryHeaders.
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

	// Refused, when set, is why this cloud will not fire some part of it. Nothing here
	// reads it: a refused target is already removed, and owes decides.
	Refused string
}

// owes is whether this schedule has anything for a slot to do. A cron that
// edges to nothing this cloud serves is never opened at all.
func (s Schedule) owes() bool { return len(s.Fires)+len(s.Starts) > 0 }

// WithSchedules is where this service reads the clock's work from, injected because
// only a composer holding a worker can enumerate crons. Without it no clock runs.
func WithSchedules(of func(context.Context) []Schedule) Option {
	return func(o *options) error {
		if of == nil {
			return errors.New("primitives: a nil schedule source")
		}
		o.schedules = of
		return nil
	}
}

// WithFire is how a fire reaches its workload, through placement, so fires spread
// across the fleet instead of running on the leader.
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
	// WITHOUT ONE A FAILURE IS IMMEDIATE, which instance/dispatch.go records as a
	// real defect: a consumer failing in a millisecond burns every attempt in a
	// millisecond and the work is in the dead letter before anybody could have
	// fixed anything.
	fireRetry = 30 * time.Second

	// fireRetryFactor and fireRetryCeiling back a failing target off rather
	// than hammering it at a fixed rate for its whole budget.
	fireRetryFactor  = 2
	fireRetryCeiling = 10 * time.Minute

	// taskTimeout bounds one tasking end to end. A window not generated within its minute
	// is re-tasked next turn anyway.
	taskTimeout = time.Minute
)

// The three values `on_overlap` takes, as the vocabulary spells them. `skip` is
// its default and is the only one that cannot make things worse.
const (
	overlapSkip  = specs.CronOnOverlapSkip
	overlapQueue = specs.CronOnOverlapQueue
	overlapAllow = specs.CronOnOverlapAllow
)

// deliveryHeaders for a fired schedule: which cron (name, id and path, since a name
// is unique only in its scope) and which moment, the slot rather than now, so jitter,
// lateness and redelivery all name the run they belong to.
const (
	headerCron     = "X-Taubyte-Cron"
	headerCronID   = "X-Taubyte-Cron-Id"
	headerCronPath = "X-Taubyte-Cron-Path"
	headerFiredAt  = "X-Taubyte-Fired-At"
)

// cronParser is the vocabulary's five-field form with descriptors (@daily, @hourly).
// No seconds field: it would change what every five-field expression means.
var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// crons is the clock half of this service: the leader's tasking, and the pass a
// tasking runs.
type crons struct {
	service *Service

	// mu guards tasked, which the service's own loop writes and a test drives
	// from its own goroutine.
	mu sync.Mutex

	// tasked is the last minute this node handed out, held only in this process (see the
	// file header).
	tasked time.Time
}

// tick is everything the leader does: one tasking per new minute, the cron grain. A
// turn inside a minute already handed out does nothing.
func (c *crons) tick(ctx context.Context, now time.Time) {
	minute := now.UTC().Truncate(time.Minute)

	c.mu.Lock()
	since := c.tasked
	c.mu.Unlock()

	if since.IsZero() {
		// First sight: record the clock and task nothing, so a job deployed at noon does not
		// run last night and a new leader does not re-run its predecessor's minute.
		c.advance(minute)
		return
	}
	if !minute.After(since) {
		return
	}

	// The window (since, minute] travels, so a stalled cloud owes each schedule one run,
	// the most recent slot, not one per missed minute.
	if err := c.assign(ctx, since, minute); err != nil {
		// Not advanced: the next turn re-tasks the window, widened.
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

// assign hands one window to a node the composer picks (WithTasking), bounded so a
// hanging tasking cannot hold the leader's single periodic goroutine.
func (c *crons) assign(ctx context.Context, since, upto time.Time) error {
	tasking, stop := context.WithTimeout(ctx, taskTimeout)
	defer stop()
	return c.service.task(tasking, since, upto)
}

// accept is the one door for a tasking, local and remote. The barrier is here: a
// stale fire-queue depth would make `skip` and `queue` behave as `allow`, and a new
// leader may hold unapplied entries.
func (c *crons) accept(ctx context.Context, since, upto time.Time) error {
	cluster, err := c.service.consensus()
	if err != nil {
		return err
	}
	if !cluster.IsLeader() {
		return c.forward(ctx, cluster, since, upto)
	}
	if c.service.schedules == nil {
		// A node that may write but runs no compute has no crons to enumerate; refusing keeps
		// the window re-taskable.
		return ErrNoClock
	}
	if err := cluster.Barrier(applyTimeout); err != nil {
		return err
	}
	return c.generate(ctx, since, upto)
}

// forward sends one window on to the node that may write it, once and never back, so
// two nodes with differing views cannot bounce it.
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

// hopHeader carries how many nodes a tasking has been sent through: the call's
// route, not the window.
const hopHeader = "Tau-Primitives-Hops"

// maxHops: the leader hands the window to a primitives node, which hands it to the
// writer. A third hop would be views disagreeing.
const maxHops = 2

type hopsKey struct{}

// hopsIn reads how far a tasking has come. An unreadable or negative count is the
// ceiling, so a garbled header cannot send a window round again.
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

// generate is what a tasked node does with its window: work out which schedules owe
// a run, push what they owe, hand out what is ready. Parsing every schedule is
// arithmetic; only due schedules touch the namespace. The sweep and dead-letter bound
// for fire queues ride here, since the lifecycle pass cannot resolve them.
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

		// The sweep first, so a consumer that died holding a fire does not leave the schedule
		// reading as busy.
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

	// Every push refused means this node can no longer write; the window goes back intact
	// for the next turn.
	if pushed == 0 && refused != nil {
		return refused
	}
	return nil
}

// push is what one slot of one schedule owes, as items in its fire queue. Only `skip`
// reads first, the queue's own depth, since an item settles only once its invocation
// returns. `queue` pushes anyway, and the queue is the backlog.
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

// drain hands out this schedule's fires and does not wait: each invocation runs in a
// goroutine on the service's lifetime, so a slow job delays no other schedule and an
// ended tasking cancels nothing. One slot at a time, or `skip` and `queue` degrade to
// `allow`; a slot's targets are one run. Close cancels and waits.
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

// bound is what a fire queue's dead letter keeps; nothing else reaches one (see
// DefaultDeadLetterRetention).
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

// owed is everything one slot of one schedule owes, one item per thing owed so each
// target gets its own dedup, retry and dead-letter place. A workflow start is an item
// too, so a refused send is retried. A target nothing can encode is dropped.
func owed(schedule Schedule, slot time.Time) []queue.Item {
	items := make([]queue.Item, 0, len(schedule.Fires)+len(schedule.Starts))

	add := func(id string, fire *primitivesv2.CronFire, what string) {
		fire.Cron, fire.Slot, fire.Jitter = schedule.Name, slot.Unix(), int64(schedule.Jitter)
		body, err := proto.Marshal(fire)
		if err != nil {
			logger.Warnf("cron: %s owes %s, which nothing can encode: %s", schedule.Name, what, err)
			return
		}
		items = append(items, queue.Item{ID: id, Data: body})
	}

	for _, target := range schedule.Fires {
		add(fireID(schedule, slot, target.key()), &primitivesv2.CronFire{
			Owes: &primitivesv2.CronFire_Target{Target: &primitivesv2.CronTarget{
				Project:     target.Project,
				Environment: target.Environment,
				Scope:       target.Scope,
				Kind:        target.Kind,
				Name:        target.Name,
			}},
		}, target.Kind+"/"+target.Name)
	}

	for _, start := range schedule.Starts {
		// The handle travels and the grant is rebuilt; a start needs only the queue it lands
		// in (see invoke.StartGrant).
		add(startID(schedule, slot, start.Handle), &primitivesv2.CronFire{
			Owes: &primitivesv2.CronFire_Start{Start: &primitivesv2.CronStart{
				Handle: start.Handle,
				Label:  start.Label,
			}},
		}, "a run of "+start.Label)
	}

	return items
}

// run is one fire: wait out the jitter, invoke, settle. 2xx acknowledges; anything
// else is nacked so the retry interval spaces the next attempt.
func (c *crons) run(ctx context.Context, held *queue.Queue, schedule Schedule, item queue.Delivery) {
	fire := &primitivesv2.CronFire{}
	if err := proto.Unmarshal(item.Data, fire); err != nil || fire.GetOwes() == nil {
		// An unreadable body is discarded, not retried into the dead letter.
		logger.Warnf("cron: %s fired an item nothing can read: %s", schedule.Name, item.ID)
		c.settle(held, schedule, item, true)
		return
	}

	// The claim's deadline bounds the fire, as a duration measured on the service's
	// injectable clock, since the deadline was stamped on that clock and not the wall
	// clock context.WithDeadline compares against.
	budget := item.Deadline.Sub(c.service.clock())
	if budget <= 0 {
		// The claim expired between handing it out and getting here. Somebody
		// else has it or is about to; giving it back now is the honest answer.
		c.settle(held, schedule, item, false)
		return
	}
	runCtx, stop := context.WithTimeout(ctx, budget)
	defer stop()

	// Jitter is waited out after the claim, so the slot is not left unclaimed for another
	// leader to hand out. fireVisibility covers this wait.
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

// invoke is a fire that names a workload, and answers whether it was done. The
// identity is read off the live schedule, not the item, so a rename reports the
// current name.
func (c *crons) invoke(ctx context.Context, schedule Schedule, fire *primitivesv2.CronFire) bool {
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
func (c *crons) begin(ctx context.Context, fire *primitivesv2.CronFire, start *primitivesv2.CronStart) bool {
	// Tier.Send reads the declaration from the handle; the rest of the grant only words a
	// refusal.
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

// settle ends one fire. A refused settle means the work will fire again, so it is
// logged.
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

// open is this schedule's own fire queue, opened per call so a redeploy's budget and
// jitter take effect.
func (c *crons) open(schedule Schedule) (*queue.Queue, error) {
	group, err := c.service.work.ready()
	if err != nil {
		return nil, err
	}
	return group.Open(fireConfig(schedule))
}

// fireConfig is the budget a fire runs under. It is the platform's because a cron's
// target declares nothing; the vocabulary has the `queue/` words but nowhere to put
// them for a cron.
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

// fireID makes a slot pushed twice one item while the first is live; never parsed.
func fireID(schedule Schedule, slot time.Time, what string) string {
	return schedule.Handle + "@" + strconv.FormatInt(slot.Unix(), 10) + "#" + what
}

// startID deduplicates the fire within the schedule's queue (carrying the handle, so
// two workflows in one slot are two items); startRunID deduplicates the run in the
// workflow's first queue (cron and slot, so a redelivered fire is one run).
func startID(schedule Schedule, slot time.Time, handle string) string {
	return fireID(schedule, slot, "start/"+handle)
}

func startRunID(cron string, slot time.Time) string {
	return cron + "@" + strconv.FormatInt(slot.Unix(), 10)
}

// slotAt is the most recent scheduled moment in (since, upto], in the schedule's
// zone, or nothing. It walks forward because a cron library answers "when next", so a
// gap is caught up to its latest slot in one step. A window past maxLateness is not
// walked.
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
