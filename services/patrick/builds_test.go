package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	commonIface "github.com/taubyte/tau/core/services/patrick"
	tauConfig "github.com/taubyte/tau/pkg/config"
	"github.com/taubyte/tau/pkg/raft"
)

type listQueue struct {
	raft.Queue
	mu  sync.Mutex
	ids []string
}

func (q *listQueue) Pop(time.Duration) (string, []byte, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.ids) == 0 {
		return "", nil, raft.ErrQueueEmpty
	}
	id := q.ids[0]
	q.ids = q.ids[1:]
	return id, nil, nil
}

func (q *listQueue) Push(id string, _ []byte, _ time.Duration) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.ids = append(q.ids, id)
	return nil
}

func (d *fakeJobs) List(_ context.Context, prefix string) ([]string, error) {
	d.lock.Lock()
	defer d.lock.Unlock()
	var keys []string
	for k := range d.keys {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	return keys, nil
}

func (d *fakeJobs) Put(_ context.Context, key string, v []byte) error {
	d.lock.Lock()
	defer d.lock.Unlock()
	d.keys[key] = v
	return nil
}

type quotaConfig struct {
	tauConfig.Config
	q tauConfig.Quotas
}

func (c quotaConfig) Quotas() tauConfig.Quotas { return c.q }

func claim(t *testing.T, db *fakeJobs, jid, project string, at time.Time) {
	t.Helper()
	data, err := cbor.Marshal(Assignment{MonkeyPID: "m", Timestamp: at.Unix(), Project: project})
	if err != nil {
		t.Fatal(err)
	}
	db.Put(context.Background(), "/assigned/"+jid, data)
}

func capped(t *testing.T, builds int, queued ...string) (*PatrickService, *fakeJobs, *listQueue) {
	db := &fakeJobs{keys: map[string][]byte{}}
	q := &listQueue{ids: queued}
	p := &PatrickService{db: db, jobQueue: q, config: quotaConfig{q: tauConfig.Quotas{Default: tauConfig.Caps{Builds: builds}}}}
	for _, id := range queued {
		data, _ := cbor.Marshal(&commonIface.Job{Id: id})
		db.keys["/jobs/"+id] = data
		p.projects.Store(id, id[:1]) // job a1 belongs to project a
	}
	return p, db, q
}

// A build another Patrick handed out (only in the replicated /assigned) holds
// its project's slot; the capped job waits and the next project's job is given.
func TestDequeueSkipsProjectAtBuildCap(t *testing.T) {
	p, db, q := capped(t, 1, "a2", "b1")
	claim(t, db, "a1", "a", time.Now())

	resp, err := p.dequeueHandler(context.Background(), &mockConnection{})
	if err != nil {
		t.Fatal(err)
	}
	if job, _ := resp["job"].(*commonIface.Job); job == nil || job.Id != "b1" {
		t.Fatalf("want b1 handed out, got %v", resp)
	}
	if len(q.ids) != 1 || q.ids[0] != "a2" {
		t.Fatalf("a2 must go back to the queue, queue is %v", q.ids)
	}

	// a1 finishes: its assignment is gone, so a2 runs.
	delete(db.keys, "/assigned/a1")
	if resp, _ = p.dequeueHandler(context.Background(), &mockConnection{}); resp["available"] != true {
		t.Fatalf("a2 must run once a1 is done, got %v", resp)
	}
}

// Usage counts the project's claimed jobs and shows its caps, only to a writer of its repositories.
func TestUsage(t *testing.T) {
	jobs := &fakeJobs{keys: map[string][]byte{}}
	addJob(t, jobs, "/jobs/", "a1", 21)
	addJob(t, jobs, "/jobs/", "a2", 21)
	db := &listJobs{fakeJobs: jobs, listed: map[string][]string{
		"/by/project/a/": {"/by/project/a/a1", "/by/project/a/a2"},
		"/assigned/":     {"/assigned/a1", "/assigned/b1"},
	}}
	srv := &PatrickService{db: db, config: quotaConfig{q: tauConfig.Quotas{Default: tauConfig.Caps{Builds: 2}}}}
	handler := srv.withProjectAccess(srv.usageHandler)

	got, err := handler(accessCtx("usage-writer", &fakeRepos{perms: map[string]map[string]bool{"21": {"push": true}}}, map[string]any{"projectId": "a"}))
	if err != nil {
		t.Fatal(err)
	}
	usage := got.(map[string]any)
	if usage["builds_running"] != 1 || usage["caps"].(tauConfig.Caps).Builds != 2 {
		t.Fatalf("got %v", usage)
	}

	if _, err := handler(accessCtx("usage-stranger", &fakeRepos{perms: map[string]map[string]bool{"21": {"pull": true}}}, map[string]any{"projectId": "a"})); !errors.Is(err, errNoJobAccess) {
		t.Fatalf("a stranger must be refused, got %v", err)
	}
}

// listTogether makes concurrent /assigned reads overlap: each waits for the
// other, or gives up when the other cannot get there.
type listTogether struct {
	*fakeJobs
	entered atomic.Int32
	both    chan struct{}
}

func (d *listTogether) List(ctx context.Context, prefix string) ([]string, error) {
	keys, err := d.fakeJobs.List(ctx, prefix)
	if d.entered.Add(1) == 2 {
		close(d.both)
	}
	select {
	case <-d.both:
	case <-time.After(200 * time.Millisecond):
	}
	return keys, err
}

// Cap 2 with 1 running: of two dequeues at the same moment, only one gets a build.
func TestConcurrentDequeuesRespectBuildCap(t *testing.T) {
	p, db, q := capped(t, 2, "a2", "a3")
	claim(t, db, "a1", "a", time.Now())
	p.db = &listTogether{fakeJobs: db, both: make(chan struct{})}

	var given atomic.Int32
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := p.dequeueHandler(context.Background(), &mockConnection{})
			if err != nil {
				t.Error(err)
			}
			if resp["available"] == true {
				given.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := given.Load(); got != 1 {
		t.Fatalf("cap 2 with 1 running allows one more build, %d were handed out", got)
	}
	if len(q.ids) != 1 {
		t.Fatalf("the job over the cap must go back to the queue, queue is %v", q.ids)
	}
}

// A dead monkey's expired claim, or the job's own claim, does not hold a slot.
func TestDequeueIgnoresStaleAndOwnClaims(t *testing.T) {
	p, db, _ := capped(t, 1, "a2")
	claim(t, db, "a1", "a", time.Now().Add(-time.Hour))
	claim(t, db, "a2", "a", time.Now())
	if resp, _ := p.dequeueHandler(context.Background(), &mockConnection{}); resp["available"] != true {
		t.Fatalf("a2 must be handed out, got %v", resp)
	}
}
