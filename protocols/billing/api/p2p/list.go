package p2p

import (
	"fmt"

	"github.com/taubyte/go-interfaces/p2p/streams"
	"github.com/taubyte/utils/maps"
)

func (c *Client) List() ([]string, error) {
	resp, err := c.client.Send("customers", streams.Body{"action": "list"})
	if err != nil {
		return nil, fmt.Errorf("failed calling send with error: %w", err)
	}

	ids, err := maps.StringArray(resp, "ids")
	if err != nil {
		return nil, fmt.Errorf("failed list string array with error: %v", err)
	}

	return ids, nil
}
