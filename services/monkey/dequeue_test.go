package monkey

import (
	"bufio"
	"errors"
	"strings"
	"sync"
	"testing"

	log "github.com/ipfs/go-log/v2"

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

// When every Patrick fails, builds stop: the round says so once at Warn, not
// once per peer.
func TestFirstJobWarnsOncePerRoundWhenAllFail(t *testing.T) {
	log.SetLogLevel("tau.monkey.service", "debug")
	t.Cleanup(func() { log.SetLogLevel("tau.monkey.service", "error") })
	pipe := log.NewPipeReader(log.PipeFormat(log.JSONOutput), log.PipeLevel(log.LevelDebug))
	var (
		wg    sync.WaitGroup
		warns int
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(pipe)
		for sc.Scan() {
			if strings.Contains(sc.Text(), `"level":"warn"`) {
				warns++
			}
		}
	}()

	peers := []peer.ID{"p1", "p2", "p3"}
	const rounds = 2
	for range rounds {
		if got := firstJob(peers, func(peer.ID) (*iface.Job, error) { return nil, errors.New("stream reset") }); got != nil {
			t.Fatalf("got %v", got)
		}
	}
	// one failing peer among answering ones is not worth a Warn
	firstJob(peers, func(p peer.ID) (*iface.Job, error) {
		if p == "p1" {
			return nil, errors.New("stream reset")
		}
		return nil, nil
	})
	pipe.Close()
	wg.Wait()

	if warns != rounds {
		t.Fatalf("%d Warn entries, want %d", warns, rounds)
	}
}
