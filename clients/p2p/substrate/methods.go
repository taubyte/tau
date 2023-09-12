package substrate

import (
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/p2p/streams/command"
	"github.com/taubyte/p2p/streams/command/response"
)

func (c *Client) Has(host, path, method string, threshold int) (map[peer.ID]response.Response, map[peer.ID]error, error) {
	body := make(map[string]interface{}, 3)
	body["host"], body["path"], body["method"] = host, path, method

	return c.streamClient.MultiSend("has", body, threshold)
}

func (c *Client) Handle(pid peer.ID) (response.Response, error) {
	return c.streamClient.SendToPID(pid, "handle", command.Body{})
}
