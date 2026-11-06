package monkey

import (
	"context"
	"testing"
	"time"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	libp2pPeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/tau/p2p/peer"
	protocolCommon "github.com/taubyte/tau/services/common"
)

// Patrick takes a job's log only from the peer the job was assigned to, which
// is the client node Monkey dequeues through. Chunks published from the main
// node were all refused on every node that runs a client node.
func TestLogChunksGoOutThroughTheClientNode(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	main, client := peer.Mock(ctx), peer.Mock(ctx)
	defer main.Close()
	defer client.Close()

	from := make(chan libp2pPeer.ID, 1)
	if err := client.PubSubSubscribe(protocolCommon.LiveLogsTopic, func(msg *pubsub.Message) {
		select {
		case from <- msg.GetFrom():
		default:
		}
	}, func(error) {}); err != nil {
		t.Fatal(err)
	}

	m := &Monkey{Id: "job1", Service: &Service{ctx: ctx, node: main, clientNode: client}}
	m.publishLogChunk(protocolCommon.LiveLogChunk{Job: "job1", Data: []byte("step\n")})

	select {
	case id := <-from:
		if id != client.ID() {
			t.Fatalf("chunk signed by %s, want the client node %s (main is %s)", id, client.ID(), main.ID())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no chunk published through the client node")
	}
}
