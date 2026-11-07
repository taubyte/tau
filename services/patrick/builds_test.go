package service

import (
	"context"
	"strings"
	"sync"
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

// A dead monkey's expired claim, or the job's own claim, does not hold a slot.
func TestDequeueIgnoresStaleAndOwnClaims(t *testing.T) {
	p, db, _ := capped(t, 1, "a2")
	claim(t, db, "a1", "a", time.Now().Add(-time.Hour))
	claim(t, db, "a2", "a", time.Now())
	if resp, _ := p.dequeueHandler(context.Background(), &mockConnection{}); resp["available"] != true {
		t.Fatalf("a2 must be handed out, got %v", resp)
	}
}
