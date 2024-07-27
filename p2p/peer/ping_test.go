package peer

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	keypair "github.com/taubyte/tau/p2p/keypair"
)

func TestPingPeer(t *testing.T) {
	ctx := context.Background()

	dir1, err := t.TempDir("", "peerRoot1")
	if err != nil {
		t.Error(err)
		return
	}
	defer os.RemoveAll(dir1)

	dir2, err := t.TempDir("", "peerRoot2")
	if err != nil {
		t.Error(err)
		return
	}
	defer os.RemoveAll(dir2)

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

	_, _, err = p1.Ping(p2.ID().String(), 1)
	if err != nil {
		t.Errorf("Ping test returned error `%s`", err.Error())
	}

	p1.Close()
	time.Sleep(3 * time.Second)
	p2.Close()
}
