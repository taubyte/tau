package gateway

import (
	"net/http"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/p2p/streams/command/response"
)

func (g *Gateway) discover(r *http.Request) (map[peer.ID]response.Response, map[peer.ID]error, error) {
	body := make(map[string]interface{}, 3)
	body["host"], body["path"], body["method"] = r.Host, r.URL.Path, r.Method

	// P2P needs a time out, can update SendToPeerTimeout, but would rather not update global var
	return g.substrateClient.MultiSend("has", body, g.threshold())
}

func (g *Gateway) threshold() int {
	thresh := int(g.connectedSubstrate)
	if thresh < 1 {
		return 1
	}
	return thresh
}
