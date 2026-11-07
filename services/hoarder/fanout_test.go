package hoarder

import (
	"bytes"
	"io"
	"sync"
	"testing"
	"time"

	peerCore "github.com/libp2p/go-libp2p/core/peer"
	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
	"github.com/taubyte/tau/p2p/peer"
)

type countingStash struct {
	hoarderIface.Client
	mu     sync.Mutex
	target peerCore.ID
	pushes map[peerCore.ID]int
}

func (c *countingStash) Peers(pids ...peerCore.ID) hoarderIface.Client {
	return &countingStash{Client: c.Client, target: pids[0], pushes: c.pushes, mu: sync.Mutex{}}
}

var pushesMu sync.Mutex

func (c *countingStash) Stash(_ string, r io.Reader, _ ...hoarderIface.StashOption) error {
	io.Copy(io.Discard, r)
	pushesMu.Lock()
	c.pushes[c.target]++
	pushesMu.Unlock()
	return nil
}

// A built function (no owner) goes to every node, and members that already
// claim it are not sent the bytes again.
func TestFanoutSendsUnownedBytesOnlyToNonHolders(t *testing.T) {
	srv := newTestService(t)
	ctx := t.Context()

	others := make([]peerCore.ID, 4)
	for i := range others {
		n := peer.Mock(ctx)
		defer n.Close()
		others[i] = n.ID()
	}
	srv.membersLock.Lock()
	if srv.members == nil {
		srv.members = map[string]*member{}
	}
	for _, id := range others {
		srv.members[id.String()] = &member{lastSeen: time.Now()}
	}
	srv.membersLock.Unlock()

	cid, err := srv.node.AddFileForCid(bytes.NewReader([]byte("built module")))
	if err != nil {
		t.Fatal(err)
	}
	// Two of the four other members already hold it.
	for _, id := range others[:2] {
		if err := srv.addStashClaim(ctx, cid.String(), id.String()); err != nil {
			t.Fatal(err)
		}
	}

	pushes := map[peerCore.ID]int{}
	srv.stashClient = &countingStash{pushes: pushes}

	srv.fanoutStash(ctx, cid.String(), stashTargetFor("", srv.fleetSize()), "")

	for _, id := range others[:2] {
		if pushes[id] != 0 {
			t.Errorf("holder %s was sent the bytes again", id)
		}
	}
	for _, id := range others[2:] {
		if pushes[id] != 1 {
			t.Errorf("non-holder %s got %d pushes, want 1", id, pushes[id])
		}
	}
}
