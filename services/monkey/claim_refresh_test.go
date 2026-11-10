package monkey

import (
	"context"
	"errors"
	"io"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"

	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
	"github.com/taubyte/tau/core/services/patrick"
	"github.com/taubyte/tau/p2p/peer"
	protocolCommon "github.com/taubyte/tau/services/common"
)

// claimPatrick is a Patrick that records what a Monkey reports. Refresh is
// S.2a's new client method; it is defined here, so this file compiles against
// a client interface that lacks it, and a Monkey that never calls it fails.
type claimPatrick struct {
	patrick.Client

	lock         sync.Mutex
	refreshes    int
	refreshOwned bool
	assigned     bool
	assignedErr  error
	done, failed []string
}

func (p *claimPatrick) Refresh(jid string) (bool, error) {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.refreshes++
	return p.refreshOwned, nil
}

func (p *claimPatrick) IsAssigned(string) (bool, error) {
	p.lock.Lock()
	defer p.lock.Unlock()
	return p.assigned, p.assignedErr
}

func (p *claimPatrick) Done(jid string, _, _ map[string]string) error {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.done = append(p.done, jid)
	return nil
}

func (p *claimPatrick) Failed(jid string, _, _ map[string]string, _ string) error {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.failed = append(p.failed, jid)
	return nil
}

func (p *claimPatrick) count() int {
	p.lock.Lock()
	defer p.lock.Unlock()
	return p.refreshes
}

type nopHoarder struct{ hoarderIface.Client }

func (nopHoarder) Stash(string, io.Reader, ...hoarderIface.StashOption) error { return nil }

// buildBlocker holds a job's build until the test ends: the build's first step
// (RunJob creating its auth client) asks the client node for its context, and
// this node never answers; at cleanup the build goroutine exits.
type buildBlocker struct {
	peer.Node
	once    sync.Once
	started chan struct{}
	release chan struct{}
}

func (n *buildBlocker) Context() context.Context {
	n.once.Do(func() { close(n.started) })
	<-n.release
	runtime.Goexit()
	return nil
}

func newClaimMonkey(t *testing.T, p *claimPatrick, repoID int, clientNode peer.Node) *Monkey {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	node := peer.Mock(ctx)
	t.Cleanup(func() { node.Close() })

	logFile, err := os.CreateTemp(t.TempDir(), "log-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { logFile.Close() })

	s := &Service{ctx: ctx, node: node, clientNode: clientNode, patrickClient: p, hoarderClient: nopHoarder{}, monkeys: map[string]*Monkey{}}
	job := &patrick.Job{Id: "j1", Logs: map[string]string{}, AssetCid: map[string]string{}}
	job.Meta.Repository.ID = repoID
	m := &Monkey{Id: job.Id, Status: patrick.JobStatusLocked, Service: s, Job: job, logFile: logFile, start: time.Now()}
	m.ctx, m.ctxC = context.WithCancel(ctx)
	return m
}

// While a build runs, the Monkey renews its claim every DefaultRefreshLockTime,
// and stops building once Patrick says the job is someone else's.
func TestMonkeyRunRefreshesClaim(t *testing.T) {
	old := protocolCommon.DefaultRefreshLockTime
	protocolCommon.DefaultRefreshLockTime = 50 * time.Millisecond
	t.Cleanup(func() { protocolCommon.DefaultRefreshLockTime = old })

	run := func(t *testing.T, owned bool) (*Monkey, *claimPatrick) {
		blocker := &buildBlocker{started: make(chan struct{}), release: make(chan struct{})}
		t.Cleanup(func() { close(blocker.release) })
		p := &claimPatrick{refreshOwned: owned, assigned: true}
		m := newClaimMonkey(t, p, 42, blocker) // repo ID > 1: a real build, held by blocker
		go m.Run()
		select {
		case <-blocker.started:
		case <-time.After(5 * time.Second):
			t.Fatal("build never started")
		}
		return m, p
	}

	t.Run("owner keeps renewing", func(t *testing.T) {
		m, p := run(t, true)
		for deadline := time.Now().Add(2 * time.Second); p.count() < 3; time.Sleep(10 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("%d Refresh calls in 2 s of building with a %v interval, want >= 3", p.count(), protocolCommon.DefaultRefreshLockTime)
			}
		}
		if m.ctx.Err() != nil {
			t.Error("build cancelled although Patrick confirmed the claim")
		}
	})

	t.Run("claim lost cancels the build", func(t *testing.T) {
		m, p := run(t, false)
		select {
		case <-m.ctx.Done():
		case <-time.After(2 * time.Second):
			t.Fatalf("build still running 2 s after Patrick answered Refresh=false (%d calls)", p.count())
		}
	})
}

// A Monkey whose ownership check fails on the network still reports its
// result, and lets Patrick's owner check decide; only a clear "not yours"
// discards it.
func TestMonkeyKeepsResultOnAssignCheckError(t *testing.T) {
	for _, tc := range []struct {
		name     string
		assigned bool
		err      error
		report   bool
	}{
		{"transport error", false, errors.New("stream reset"), true},
		{"not assigned", false, nil, false},
		{"assigned", true, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &claimPatrick{refreshOwned: true, assigned: tc.assigned, assignedErr: tc.err}
			m := newClaimMonkey(t, p, 1, nil) // repo ID 1: the build succeeds at once
			m.Run()

			p.lock.Lock()
			defer p.lock.Unlock()
			reported := len(p.done)+len(p.failed) > 0
			if reported != tc.report {
				t.Fatalf("IsAssigned=(%v, %v): reported=%v (done %v, failed %v), want %v", tc.assigned, tc.err, reported, p.done, p.failed, tc.report)
			}
			if tc.report && (len(p.done) != 1 || m.Status != patrick.JobStatusSuccess) {
				t.Errorf("successful build reported done=%v failed=%v status=%v, want Done once", p.done, p.failed, m.Status)
			}
		})
	}
}
