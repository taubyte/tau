package hoarder

import (
	"sort"
	"testing"
	"time"

	hoarderSpecs "github.com/taubyte/tau/pkg/specs/hoarder"
)

// drain empties a buffered trigger channel between assertions.
func drain(ch chan string) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

func TestObserveHeartbeat(t *testing.T) {
	srv := newTestService(t)
	srv.reconcileTrigger = make(chan string, 8)
	self := srv.node.ID().String()

	// Our own heartbeat is never a "member" of ourselves and must not churn.
	srv.observeHeartbeat(&heartbeat{PeerID: self, Incarnation: 1})
	if len(srv.members) != 0 {
		t.Fatalf("self heartbeat should not add a member, got %d", len(srv.members))
	}
	if len(srv.reconcileTrigger) != 0 {
		t.Fatal("self heartbeat should not fire a membership change")
	}

	// A new peer is added and fires a membership change (placement may shift).
	srv.observeHeartbeat(&heartbeat{PeerID: "peerA", Incarnation: 10})
	if _, ok := srv.members["peerA"]; !ok {
		t.Fatal("a new peer should be added")
	}
	if len(srv.reconcileTrigger) != 1 {
		t.Fatal("a new member must fire a membership change")
	}
	drain(srv.reconcileTrigger)

	// An older incarnation's straggling beat is ignored (no overwrite, no fire).
	srv.observeHeartbeat(&heartbeat{PeerID: "peerA", Incarnation: 5})
	if srv.members["peerA"].hb.Incarnation != 10 {
		t.Fatal("an older incarnation must not overwrite a newer one")
	}
	if len(srv.reconcileTrigger) != 0 {
		t.Fatal("a straggling older beat must not fire a change")
	}

	// A newer incarnation of a KNOWN member updates in place without re-firing.
	srv.observeHeartbeat(&heartbeat{PeerID: "peerA", Incarnation: 20})
	if srv.members["peerA"].hb.Incarnation != 20 {
		t.Fatal("a newer incarnation should update the member")
	}
	if len(srv.reconcileTrigger) != 0 {
		t.Fatal("re-beat of a known member should not fire a change")
	}
}

func TestExpireDead(t *testing.T) {
	srv := newTestService(t)
	srv.reconcileTrigger = make(chan string, 8)

	srv.members["fresh"] = &member{hb: heartbeat{PeerID: "fresh"}, lastSeen: time.Now()}
	srv.members["stale"] = &member{hb: heartbeat{PeerID: "stale"}, lastSeen: time.Now().Add(-2 * hoarderSpecs.LivenessTimeout)}

	srv.expireDead()

	if _, ok := srv.members["fresh"]; !ok {
		t.Fatal("a fresh member must survive")
	}
	if _, ok := srv.members["stale"]; ok {
		t.Fatal("a stale member must be expired")
	}
	if len(srv.reconcileTrigger) != 1 {
		t.Fatal("expiring a member must fire a membership change")
	}

	// A sweep with nothing to expire must not fire.
	drain(srv.reconcileTrigger)
	srv.expireDead()
	if len(srv.reconcileTrigger) != 0 {
		t.Fatal("a no-op expiry sweep must not fire a change")
	}
}

func TestActiveMembers(t *testing.T) {
	srv := newTestService(t)
	self := srv.node.ID().String()

	// With no peers, only self — and self is always present.
	if got := srv.activeMembers(); len(got) != 1 || got[0] != self {
		t.Fatalf("activeMembers with no peers = %v, want [self]", got)
	}

	srv.members["zpeer"] = &member{hb: heartbeat{PeerID: "zpeer"}, lastSeen: time.Now()}
	srv.members["apeer"] = &member{hb: heartbeat{PeerID: "apeer"}, lastSeen: time.Now()}
	srv.members["dead"] = &member{hb: heartbeat{PeerID: "dead"}, lastSeen: time.Now().Add(-2 * hoarderSpecs.LivenessTimeout)}

	got := srv.activeMembers()
	if !sort.StringsAreSorted(got) {
		t.Fatalf("activeMembers must be sorted, got %v", got)
	}
	if contains(got, "dead") {
		t.Fatal("an expired member must not appear in activeMembers")
	}
	if len(got) != 3 || !contains(got, self) || !contains(got, "apeer") || !contains(got, "zpeer") {
		t.Fatalf("activeMembers = %v, want [self apeer zpeer]", got)
	}
}

func TestMembershipEpoch(t *testing.T) {
	srv := newTestService(t)

	e1 := srv.membershipEpoch()
	if e1 == "" {
		t.Fatal("epoch must be non-empty")
	}
	if e1 != srv.membershipEpoch() {
		t.Fatal("epoch must be stable for an unchanged set")
	}

	// A membership change must change the epoch.
	srv.members["peerA"] = &member{hb: heartbeat{PeerID: "peerA"}, lastSeen: time.Now()}
	if srv.membershipEpoch() == e1 {
		t.Fatal("epoch must change when the active set changes")
	}
}

func TestJoinSorted(t *testing.T) {
	if joinSorted(nil) != "" {
		t.Fatal("joinSorted(nil) must be empty")
	}
	if got := joinSorted([]string{"a", "b"}); got != "a\x00b\x00" {
		t.Fatalf("joinSorted = %q, want a\\x00b\\x00", got)
	}
}

func TestCurrentHolders(t *testing.T) {
	srv := newTestService(t)
	ctx := t.Context()
	self := srv.node.ID().String()
	hash := instanceHash(meta("holders"))

	// self claims (and is always a live member); a ghost peer claims but is not
	// a live member, so it must be filtered out of the holder set.
	srv.addClaim(ctx, hash, self)       //nolint:errcheck
	srv.addClaim(ctx, hash, "ghostPid") //nolint:errcheck

	holders, err := srv.currentHolders(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	if len(holders) != 1 || holders[0] != self {
		t.Fatalf("currentHolders = %v, want [self] (ghost is not a live member)", holders)
	}

	// Once the ghost is a live member, it counts as a holder.
	srv.members["ghostPid"] = &member{hb: heartbeat{PeerID: "ghostPid"}, lastSeen: time.Now()}
	holders, _ = srv.currentHolders(ctx, hash)
	if len(holders) != 2 {
		t.Fatalf("currentHolders with a live ghost = %v, want 2", holders)
	}
}
