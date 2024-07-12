package monkey

import (
	"fmt"

	"github.com/taubyte/tau/p2p/streams/command"
	"github.com/taubyte/utils/maps"
)

func (c *Client) List() ([]string, error) {
	resp, err := c.client.Send("job", command.Body{"action": "list"}, c.peers...)
	if err != nil {
		return nil, fmt.Errorf("failed calling job with error: %w", err)
	}

	ids, err := maps.StringArray(resp, "ids")
	if err != nil {
		return nil, fmt.Errorf("failed list string array with error: %w", err)
	}

	return ids, nil
}
