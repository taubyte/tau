package tests

// TODO: Redo this test
import (
	"context"
	"fmt"
	"testing"
	"time"

	peercore "github.com/libp2p/go-libp2p/core/peer"

	keypair "github.com/taubyte/tau/p2p/keypair"

	hoarder_client "github.com/taubyte/tau/clients/p2p/hoarder"
	"github.com/taubyte/tau/config"

	peer "github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/services/common"
	service "github.com/taubyte/tau/services/hoarder"
)

func TestHoarderClient(t *testing.T) {
	ctx := context.Background()

	srvRoot := t.TempDir()

	srv, err := service.New(ctx, &config.Node{
		Root:        srvRoot,
		P2PListen:   []string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", 11010)},
		P2PAnnounce: []string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", 11010)},
		SwarmKey:    common.SwarmKey(),
		DevMode:     true,
	})

	if err != nil {
		t.Errorf("Error creating Service with: %s", err)
		return
	}
	defer srv.Close()

	peerC, err := peer.New( // consumer
		ctx,
		nil,
		keypair.NewRaw(),
		common.SwarmKey(),
		[]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", 11012)},
		nil,
		true,
		false,
	)

	if err != nil {
		t.Errorf("Creating new peer error `%s`", err.Error())
		return
	}

	// give service some time to start
	time.Sleep(1 * time.Second)

	err = peerC.Peer().Connect(ctx, peercore.AddrInfo{ID: srv.Node().ID(), Addrs: srv.Node().Peer().Addrs()})
	if err != nil {
		t.Errorf("Connect to peer returned `%s`", err.Error())
		return
	}

	// give time for peers to discover each other
	time.Sleep(1 * time.Second)

	// No peer
	_, err = hoarder_client.New(ctx, peerC)
	if err != nil {
		t.Error(err)
		return
	}
}
