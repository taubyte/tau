package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/taubyte/tau/pkg/raft"
)

// popQueue is a job queue whose Pop answers err.
type popQueue struct {
	raft.Queue
	err error
}

func (q popQueue) Pop(time.Duration) (string, []byte, error) { return "", nil, q.err }

// A follower cannot pop the queue. It answers that it has no job, so the
// monkey asks the next Patrick, instead of failing the monkey's whole poll.
func TestDequeueOnFollowerHasNoJob(t *testing.T) {
	for _, err := range []error{raft.ErrNotLeader, raft.ErrQueueEmpty} {
		srv := &PatrickService{jobQueue: popQueue{err: err}}
		resp, got := srv.dequeueHandler(context.Background(), nil)
		if got != nil {
			t.Fatalf("%v: got error %v", err, got)
		}
		if resp["available"] != false {
			t.Fatalf("%v: got %v", err, resp)
		}
	}

	broken := errors.New("disk on fire")
	if _, got := (&PatrickService{jobQueue: popQueue{err: broken}}).dequeueHandler(context.Background(), nil); !errors.Is(got, broken) {
		t.Fatalf("a real failure must be reported, got %v", got)
	}
}
