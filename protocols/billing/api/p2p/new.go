package p2p

import (
	"fmt"

	"github.com/taubyte/go-interfaces/p2p/streams"
	"github.com/taubyte/utils/maps"
)

func (c *Client) New(project string) (string, error) {
	resp, err := c.client.Send("customers", streams.Body{"action": "new", "project": project, "provider": "stripe"})
	if err != nil {
		return "", fmt.Errorf("failed calling send with error: %w", err)
	}

	id, err := maps.String(resp, "id")
	if err != nil {
		return "", fmt.Errorf("failed new map string with error: %w", err)
	}

	return id, nil
}
