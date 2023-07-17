package p2p

import (
	"fmt"

	"github.com/mitchellh/mapstructure"
	"github.com/taubyte/go-interfaces/p2p/streams"
	"github.com/taubyte/go-interfaces/services/monkey"
)

func (c *Client) Status(jid string) (*monkey.StatusResponse, error) {
	resp, err := c.client.Send("job", streams.Body{"jid": jid, "action": "status"})
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
