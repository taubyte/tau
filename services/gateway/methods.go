package gateway

import (
	"slices"
	"time"

	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/taubyte/tau/clients/p2p/substrate"
	"github.com/taubyte/tau/p2p/peer"
	http "github.com/taubyte/tau/pkg/http"
	protocolCommon "github.com/taubyte/tau/services/common"
)

func (g *Gateway) Node() peer.Node {
	return g.node
}

func (g *Gateway) Http() http.Service {
	return g.http
}

func (g *Gateway) Close() error {
	return g.substrateClient.Close()
}

// lookupPlan sizes a lookup to the substrates this node is connected to. The
// client waits out the whole timeout for peers it does not have, so a fixed
// threshold above the real count costs every request that wait. With none
// connected the cloud is still starting: the last, long attempt would only hang.
// shortcut: counts connected peers, so a live substrate not yet dialed is found
// as the client dials it; upgrade if balance across new nodes must be instant.
func (g *Gateway) lookupPlan() (threshold int, timeouts []time.Duration) {
	timeouts = lookupTimeouts
	if g.clientNode == nil {
		return 0, timeouts
	}
	known := 0
	ps, nw := g.clientNode.Peer().Peerstore(), g.clientNode.Peer().Network()
	for _, pid := range nw.Peers() {
		if protos, err := ps.GetProtocols(pid); err == nil && slices.Contains(protos, protocol.ID(protocolCommon.SubstrateProtocol)) {
			known++
		}
	}
	if known == 0 && len(timeouts) > 1 {
		timeouts = timeouts[:len(timeouts)-1]
	}
	return max(1, min(known, substrate.DefaultThreshold)), timeouts
}
