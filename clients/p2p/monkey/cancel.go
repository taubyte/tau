package monkey

import (
	"fmt"

	"github.com/taubyte/tau/p2p/streams/command"
	"github.com/taubyte/tau/p2p/streams/command/response"
)

func (c *Client) Cancel(jid string) (response.Response, error) {
	resp, err := c.client.Send("job", command.Body{"action": "cancel", "jid": jid}, c.peers...)
	if err != nil {
		return nil, fmt.Errorf("failed calling cancelJob with error: %w", err)
	}

	return resp, nil
}
