package monkey

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/protocol"
	ma "github.com/multiformats/go-multiaddr"
	peer "github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/raft"
	protocolCommon "github.com/taubyte/tau/services/common"
)

// On a real cloud the Raft leader also runs a Monkey. Only the leader hands
// out jobs, so its Monkey must ask its own Patrick too, even though libp2p
// never lists a host own protocols in its peerstore.
func TestDiscoverIncludesLocalPatrickBesideOthers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const cluster = "main"
	protos := []protocol.ID{protocol.ID(raft.Protocol(cluster)), protocol.ID(protocolCommon.PatrickProtocol)}

	self, other := peer.Mock(ctx), peer.Mock(ctx)
	defer self.Close()
	defer other.Close()
	for _, p := range protos {
		self.Peer().SetStreamHandler(p, func(network.Stream) {})
	}
	ps := self.Peer().Peerstore()
	ps.AddAddr(other.ID(), ma.StringCast("/ip4/10.0.0.2/tcp/4242"), time.Hour)
	if err := ps.AddProtocols(other.ID(), protos...); err != nil {
		t.Fatal(err)
	}

	got := (&Service{node: self, cluster: cluster}).discoverPatrickPeers()
	if !slices.Contains(got, self.ID()) || !slices.Contains(got, other.ID()) {
		t.Fatalf("got %v, want both self %s and other %s", got, self.ID(), other.ID())
	}
}
