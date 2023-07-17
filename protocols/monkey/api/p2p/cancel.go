package p2p

import (
	"fmt"

	"bitbucket.org/taubyte/p2p/streams/command/response"
	"github.com/ipfs/go-cid"
	"github.com/taubyte/go-interfaces/p2p/streams"
)

func (c *Client) Cancel(cid cid.Cid, jid string) (response.Response, error) {
	resp, err := c.client.SendTo(cid, "job", streams.Body{"action": "cancel", "jid": jid})
	if err != nil {
		return nil, fmt.Errorf("failed calling cancelJob with error: %w", err)
	}

	return resp, nil
}
