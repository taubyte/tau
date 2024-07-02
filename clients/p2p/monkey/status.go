package monkey

import (
	"fmt"

	"github.com/mitchellh/mapstructure"
	"github.com/taubyte/tau/core/services/monkey"
	"github.com/taubyte/tau/p2p/streams/command"
)

func (c *Client) Status(jid string) (*monkey.StatusResponse, error) {
	resp, err := c.client.Send("job", command.Body{"jid": jid, "action": "status"}, c.peers...)
	if err != nil {
		return nil, fmt.Errorf("failed calling job with error: %w", err)
	}

	var result *monkey.StatusResponse
	err = mapstructure.Decode(resp, &result)
	if err != nil {
		return nil, fmt.Errorf("decoding status failed with: %s", err.Error())
	}

	return result, nil
}
