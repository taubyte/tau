package service

import (
	"errors"
	"testing"
	"time"

	"github.com/taubyte/tau/pkg/raft"
)

// pushQueue is a job queue whose Push answers err and records what it was given.
type pushQueue struct {
	raft.Queue
	err    error
	pushed []string
}

func (q *pushQueue) Push(id string, _ []byte, _ time.Duration) error {
	q.pushed = append(q.pushed, id)
	return q.err
}

// A follower cannot write the queue. The job is already stored, and the
// leader's re-announce queues it, so the webhook that brought it succeeds.
func TestEnqueueLeavesStoredJobToLeader(t *testing.T) {
	broken := errors.New("disk on fire")
	for _, c := range []struct {
		name string
		err  error
		want error
	}{
		{"leader queues it", nil, nil},
		{"follower leaves it to the leader", raft.ErrNotLeader, nil},
		{"wrapped not-leader is the same answer", errors.Join(errors.New("batch"), raft.ErrNotLeader), nil},
		{"any other failure is reported", broken, broken},
	} {
		t.Run(c.name, func(t *testing.T) {
			q := &pushQueue{err: c.err}
			srv := &PatrickService{jobQueue: q}
			if err := srv.enqueue("job-1"); !errors.Is(err, c.want) || (c.want == nil && err != nil) {
				t.Fatalf("got %v, want %v", err, c.want)
			}
			if len(q.pushed) != 1 || q.pushed[0] != "job-1" {
				t.Fatalf("pushed %v", q.pushed)
			}
		})
	}
}
