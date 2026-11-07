package service

import (
	"context"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/core/services/patrick"
	"github.com/taubyte/tau/p2p/streams/command"
	"github.com/taubyte/tau/pkg/kvdb/mock"
	"github.com/taubyte/tau/pkg/raft"
	servicesCommon "github.com/taubyte/tau/services/common"
)

// S.2a: a Monkey renews its claim on a job, so a long build is not handed to a
// second Monkey. Claim timestamps are Unix seconds, so every bound below allows
// one second of rounding.

const (
	claimJob    = "j1"
	claimOwner  = peer.ID("monkey-a")
	claimOther  = peer.ID("monkey-b")
	timeSlack   = 2 // seconds a fresh timestamp may lag now
	scanForTest = 5 * time.Second
)

func newClaimService(t *testing.T, queue raft.Queue) *PatrickService {
	t.Helper()
	db, err := mock.New().New(nil, "/test", 0)
	if err != nil {
		t.Fatal(err)
	}
	return &PatrickService{
		db:                db,
		node:              &mockNode{},
		monkeyClient:      &mockMonkeyClient{},
		jobQueue:          queue,
		reAnnounceJobTime: scanForTest,
	}
}

func storeClaimJob(t *testing.T, db kvdb.KVDB, jid string) {
	t.Helper()
	data, err := cbor.Marshal(&patrick.Job{Id: jid, Logs: map[string]string{}, AssetCid: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Put(context.Background(), "/jobs/"+jid, data); err != nil {
		t.Fatal(err)
	}
}

func putClaim(t *testing.T, db kvdb.KVDB, jid string, owner peer.ID, ts int64) {
	t.Helper()
	data, err := cbor.Marshal(Assignment{MonkeyPID: owner.String(), Timestamp: ts})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Put(context.Background(), "/assigned/"+jid, data); err != nil {
		t.Fatal(err)
	}
}

func readClaim(t *testing.T, db kvdb.KVDB, jid string) Assignment {
	t.Helper()
	data, err := db.Get(context.Background(), "/assigned/"+jid)
	if err != nil {
		t.Fatalf("no claim on %s: %v", jid, err)
	}
	var a Assignment
	if err := cbor.Unmarshal(data, &a); err != nil {
		t.Fatal(err)
	}
	return a
}

func send(srv *PatrickService, from peer.ID, body command.Body) (map[string]interface{}, error) {
	return srv.requestServiceHandler(context.Background(), &mockConnection{remotePeer: from}, body)
}

func refresh(srv *PatrickService, from peer.ID, jid string) (map[string]interface{}, error) {
	return send(srv, from, command.Body{"action": "refresh", "jid": jid})
}

func setLockTime(t *testing.T, d time.Duration) {
	t.Helper()
	old := servicesCommon.DefaultLockTime
	servicesCommon.DefaultLockTime = d
	t.Cleanup(func() { servicesCommon.DefaultLockTime = old })
}

// The owner's refresh renews the claim, so re-announce leaves the job alone.
func TestRefreshKeepsClaim(t *testing.T) {
	setLockTime(t, 60*time.Second)
	queue := &mockJobQueue{}
	srv := newClaimService(t, queue)
	storeClaimJob(t, srv.db, claimJob)
	putClaim(t, srv.db, claimJob, claimOwner, time.Now().Unix()-120)

	resp, err := refresh(srv, claimOwner, claimJob)
	if err != nil {
		t.Fatalf("refresh by the owner: %v", err)
	}
	if resp["assigned"] != true {
		t.Errorf("refresh by the owner answered %v, want assigned=true", resp)
	}

	if age := time.Now().Unix() - readClaim(t, srv.db, claimJob).Timestamp; age > timeSlack {
		t.Errorf("claim is %d s old after the owner's refresh, want <= %d s", age, timeSlack)
	}

	if err := srv.ReannounceJobs(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(queue.pushCalls) != 0 {
		t.Errorf("re-announce re-queued %v although the owner just refreshed its claim", queue.pushCalls)
	}
}

// Only the owner renews. Another peer gets assigned=false and changes nothing.
func TestRefreshByOtherPeerChangesNothing(t *testing.T) {
	srv := newClaimService(t, &mockJobQueue{})
	storeClaimJob(t, srv.db, claimJob)
	ts := time.Now().Unix() - 120
	putClaim(t, srv.db, claimJob, claimOwner, ts)

	resp, err := refresh(srv, claimOther, claimJob)
	if err != nil {
		t.Fatalf("refresh by another peer: %v", err)
	}
	if v, ok := resp["assigned"]; !ok || v != false {
		t.Errorf("refresh by another peer answered %v, want assigned=false", resp)
	}

	got := readClaim(t, srv.db, claimJob)
	if got.MonkeyPID != claimOwner.String() || got.Timestamp != ts {
		t.Errorf("claim changed to %+v, want {%s %d}", got, claimOwner, ts)
	}

	// no claim at all: nothing to renew, and none is created
	resp, err = refresh(srv, claimOther, "no-such-job")
	if err != nil {
		t.Fatalf("refresh of an unclaimed job: %v", err)
	}
	if v, ok := resp["assigned"]; !ok || v != false {
		t.Errorf("refresh of an unclaimed job answered %v, want assigned=false", resp)
	}
	if _, err := srv.db.Get(context.Background(), "/assigned/no-such-job"); err == nil {
		t.Error("refresh created a claim on an unclaimed job")
	}
}

// A claim lasts DefaultLockTime, whatever the scan interval: re-announce
// re-queues a dead Monkey's job once the claim is older than that, and never
// a live claim younger than that.
func TestReannounceReclaimsExpiredClaim(t *testing.T) {
	for _, tc := range []struct {
		name       string
		scan, lock time.Duration
	}{
		{"scan shorter than claim (Dream, S.2b)", 5 * time.Second, 60 * time.Second},
		{"scan longer than claim", 60 * time.Second, 10 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setLockTime(t, tc.lock)
			queue := &mockJobQueue{}
			srv := newClaimService(t, queue)
			srv.reAnnounceJobTime = tc.scan

			now := time.Now().Unix()
			lock := int64(tc.lock.Seconds())
			storeClaimJob(t, srv.db, "dead")
			putClaim(t, srv.db, "dead", claimOwner, now-lock-1)
			storeClaimJob(t, srv.db, "live")
			putClaim(t, srv.db, "live", claimOther, now-lock/2)

			if err := srv.ReannounceJobs(context.Background()); err != nil {
				t.Fatal(err)
			}

			pushed := map[string]bool{}
			for _, c := range queue.pushCalls {
				pushed[c.id] = true
			}
			if !pushed["dead"] {
				t.Errorf("claim %d s old (lock %v, scan %v) was not re-queued", lock+1, tc.lock, tc.scan)
			}
			if pushed["live"] {
				t.Errorf("claim %d s old (lock %v, scan %v) was re-queued while still valid", lock/2, tc.lock, tc.scan)
			}
		})
	}
}

// Two Monkeys, one job, the real raft queue: B never gets j1 while A renews
// its claim, and gets it once A stops renewing and the claim runs out.
func TestTwoMonkeysOneOwner(t *testing.T) {
	const lock = 2 * time.Second
	setLockTime(t, lock)

	srv := newClaimService(t, raft.NewQueue(raft.NewMockCluster(), "jobs"))
	srv.reAnnounceJobTime = 100 * time.Millisecond // the scan tick, much shorter than the claim
	storeClaimJob(t, srv.db, claimJob)
	if err := srv.enqueue(claimJob); err != nil {
		t.Fatal(err)
	}

	dequeue := func(who peer.ID) string {
		t.Helper()
		resp, err := send(srv, who, command.Body{"action": "dequeue"})
		if err != nil {
			t.Fatalf("dequeue as %s: %v", who, err)
		}
		if resp["available"] != true {
			return ""
		}
		job, _ := resp["job"].(*patrick.Job)
		if job == nil {
			t.Fatalf("dequeue as %s answered %v", who, resp)
		}
		return job.Id
	}

	if got := dequeue(claimOwner); got != claimJob {
		t.Fatalf("A dequeued %q, want %s", got, claimJob)
	}

	// One goroutine drives A's refresh, Patrick's re-announce and B's poll in
	// turn, so the order of events is fixed; only the clock moves.
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	step := func(aRefreshes bool) string {
		<-tick.C
		if aRefreshes {
			if _, err := refresh(srv, claimOwner, claimJob); err != nil {
				t.Fatalf("A refresh: %v", err)
			}
		}
		if err := srv.ReannounceJobs(context.Background()); err != nil {
			t.Fatal(err)
		}
		return dequeue(claimOther)
	}

	start := time.Now()
	for time.Since(start) < 2*lock {
		if got := step(true); got == claimJob {
			t.Fatalf("B got %s %v after A took it, while A was renewing its claim (lock %v)", claimJob, time.Since(start).Round(time.Millisecond), lock)
		}
	}

	// A stops renewing (crashed). +2 s: one second of timestamp rounding, one of ticks.
	stopped := time.Now()
	for deadline := stopped.Add(lock + 2*time.Second); ; {
		if step(false) == claimJob {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("B did not get %s within %v of A stopping", claimJob, lock+2*time.Second)
		}
	}
	if owner := readClaim(t, srv.db, claimJob).MonkeyPID; owner != claimOther.String() {
		t.Errorf("claim owner after B took the job is %s, want B", owner)
	}
}
