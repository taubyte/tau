package monkey

import (
	"fmt"

	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/tau/p2p/streams/command"
	"github.com/taubyte/tau/p2p/streams/command/response"
)

func (c *Client) Cancel(cid cid.Cid, jid string) (response.Response, error) {
	pid, err := peer.FromCid(cid)
	if err != nil {
		return nil, fmt.Errorf("cid to pid failed with: %w", err)
	}

	resp, err := c.client.SendTo(pid, "job", command.Body{"action": "cancel", "jid": jid})
	if err != nil {
		return nil, fmt.Errorf("failed calling cancelJob with error: %w", err)
	}

	return resp, nil
}
