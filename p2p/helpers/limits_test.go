package helpers

import (
	"testing"

	"github.com/libp2p/go-libp2p/core/protocol"
	rcmgr "github.com/libp2p/go-libp2p/p2p/host/resource-manager"
)

func TestRequestPathProtocolsHaveRoomForLoad(t *testing.T) {
	limits := cloudLimits()
	got := limits.Scale(2<<30, 4096).ToPartialLimitConfig().ProtocolPeer
	for _, proto := range []string{"/tns/v1", "/hoarder/v1"} {
		if in := got[protocol.ID(proto)].StreamsInbound; in < 1024 {
			t.Fatalf("%s allows %v inbound streams per peer", proto, in)
		}
	}
	if d := rcmgr.DefaultLimits; d.ProtocolPeerBaseLimit.StreamsInbound >= 1024 {
		t.Fatal("libp2p raised its default: this override may no longer be needed")
	}
}
