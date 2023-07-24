package p2p

import (
	"fmt"

	"github.com/taubyte/p2p/streams/command"
	"github.com/taubyte/p2p/streams/command/response"
)

func (c *Client) Stash(cid string) (response.Response, error) {
	// sends to signal a peer and tells them to stash the cid
	resp, err := c.client.Send("hoarder", command.Body{"cid": cid, "action": "stash"})
	if err != nil {
		logger.Errorf(fmt.Sprintf("Failed stashing cid %s with error: %v", cid, err))
		return nil, fmt.Errorf("failed calling send with error: %w", err)
	}

	return resp, nil
}
