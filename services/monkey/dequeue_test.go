package monkey

import (
	"errors"
	"testing"

	"github.com/libp2p/go-libp2p/core/peer"
	iface "github.com/taubyte/tau/core/services/patrick"
)

// Followers answer "nothing here" (or fail); the leader hands out the job. The
// monkey must reach the leader whatever order the Patricks come in, ask no one
// after it has a job, and pop at most once.
func TestFirstJobReachesTheLeader(t *testing.T) {
	follower1, follower2, leader := peer.ID("f1"), peer.ID("f2"), peer.ID("leader")
	job := &iface.Job{Id: "job-1"}

	for _, order := range [][]peer.ID{
		{follower1, follower2, leader},
		{leader, follower1, follower2},
		{follower1, leader, follower2},
	} {
		var asked []peer.ID
		got := firstJob(order, func(p peer.ID) (*iface.Job, error) {
			asked = append(asked, p)
			switch p {
			case leader:
				return job, nil
			case follower2:
				return nil, errors.New("stream reset")
			}
			return nil, nil
		})
		if got != job {
			t.Fatalf("order %v: got %v", order, got)
		}
		if asked[len(asked)-1] != leader {
			t.Fatalf("order %v: kept asking after the leader: %v", order, asked)
		}
	}

	if got := firstJob([]peer.ID{follower1, follower2}, func(peer.ID) (*iface.Job, error) { return nil, nil }); got != nil {
		t.Fatalf("no leader, got %v", got)
	}
}
