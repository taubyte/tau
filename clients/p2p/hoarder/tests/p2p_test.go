package tests

// TODO: Redo this test
import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"testing"
	"time"

	peercore "github.com/libp2p/go-libp2p/core/peer"

	keypair "bitbucket.org/taubyte/p2p/keypair"

	hoarder_client "github.com/taubyte/odo/clients/p2p/hoarder"
	"github.com/taubyte/odo/config"

	peer "bitbucket.org/taubyte/p2p/peer"
	service "github.com/taubyte/odo/protocols/hoarder/service"
)

func TestClient(t *testing.T) {
	ctx := context.Background()

	srvRoot, err := ioutil.TempDir("", "clientSrvRoot")
	if err != nil {
		t.Error(err)
		return
	}
	defer os.RemoveAll(srvRoot)

	srv, err := service.New(ctx, &config.Protocol{
		Root:        srvRoot,
		P2PListen:   []string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", 11010)},
		P2PAnnounce: []string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", 11010)},
		SwarmKey:    peer.DefaultSwarmKey(),
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
		peer.DefaultSwarmKey(),
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
	hoarder_client.MinPeers = 1
	_, err = hoarder_client.New(ctx, peerC)
	if err != nil {
		t.Error(err)
		return
	}
}
