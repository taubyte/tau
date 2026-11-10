package service

import (
	"errors"
	"time"

	"github.com/taubyte/tau/pkg/raft"
)

// enqueue queues a job already stored under /jobs/. Only the raft leader can
// write the queue, and a webhook reaches whichever Patrick DNS picked, so a
// follower leaves the job to the leader: its re-announce queues every stored,
// unassigned job within reAnnounceJobTime. Refusing it instead failed the
// webhook, and GitHub does not resend a failed push.
func (srv *PatrickService) enqueue(jid string) error {
	err := srv.jobQueue.Push(jid, nil, 5*time.Second)
	if errors.Is(err, raft.ErrNotLeader) {
		logger.Debugf("job %s stored; not the queue leader, its re-announce queues it", jid)
		return nil
	}
	return err
}
