package peer

import (
	"context"
	"fmt"
	"testing"
	"time"

	keypair "github.com/taubyte/tau/p2p/keypair"
)

func TestPingPeer(t *testing.T) {
	ctx := context.Background()

	dir1 := t.TempDir()

	dir2 := t.TempDir()

	p1, err := New(
		ctx,
		dir1,
		keypair.NewRaw(),
		nil,
		[]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", 11001)},
		nil,
		true,
		false,
	)

	if err != nil {
		t.Errorf("Ping test returned error `%s`", err.Error())
	}

	p2, err := New(
		ctx,
		dir2,
		keypair.NewRaw(),
		nil,
		[]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", 11002)},
		nil,
		true,
		false,
	)

	if err != nil {
		t.Errorf("Ping test returned error `%s`", err.Error())
	}

	_, _, err = p1.Ping(ctx, p2.ID().String(), 1)
	if err != nil {
		t.Errorf("Ping test returned error `%s`", err.Error())
	}

	p1.Close()

	// Let p2 observe p1's departure before closing it (best-effort).
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) && p2.Peer().Network().Connectedness(p1.ID()).String() == "Connected" {
		time.Sleep(100 * time.Millisecond)
	}

	p2.Close()
}
