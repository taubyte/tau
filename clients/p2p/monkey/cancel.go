package p2p

import (
	"fmt"

	"github.com/ipfs/go-cid"
	"github.com/taubyte/p2p/streams/command"
	"github.com/taubyte/p2p/streams/command/response"
)

func (c *Client) Cancel(cid cid.Cid, jid string) (response.Response, error) {
	resp, err := c.client.SendTo(cid, "job", command.Body{"action": "cancel", "jid": jid})
	if err != nil {
		return nil, fmt.Errorf("failed calling cancelJob with error: %w", err)
	}

	return resp, nil
}
