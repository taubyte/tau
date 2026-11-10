package monkey

import (
	"github.com/libp2p/go-libp2p/core/peer"
	iface "github.com/taubyte/tau/core/services/patrick"
)

// dequeueFromAny asks each Patrick in turn for a job and takes the first one
// handed out. Only the raft leader can pop the queue, and asking every Patrick
// at once kept whichever answer came first: a follower's "nothing here" won on
// every node but the leader's, so those monkeys never built, and a job the
// leader popped for a lost answer waited out its assignment.
func (srv *Service) dequeueFromAny(peers []peer.ID) *iface.Job {
	return firstJob(peers, func(p peer.ID) (*iface.Job, error) {
		return srv.patrickClient.Peers(p).Dequeue()
	})
}

func firstJob(peers []peer.ID, dequeue func(peer.ID) (*iface.Job, error)) *iface.Job {
	for _, p := range peers {
		job, err := dequeue(p)
		if err != nil {
			logger.Debugf("Dequeue from %s failed: %v", p, err)
			continue
		}
		if job != nil {
			return job
		}
	}
	return nil
}
