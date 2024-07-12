package hoarder

import (
	"fmt"

	"github.com/taubyte/tau/p2p/streams/command"
	"github.com/taubyte/utils/maps"
)

func (c *Client) List() ([]string, error) {
	resp, err := c.Send("hoarder", command.Body{"action": "list"}, c.peers...)
	if err != nil {
		logger.Error("Failed listing cids with error:", err.Error())
		return nil, fmt.Errorf("failed calling send with error: %w", err)
	}

	ids, err := maps.StringArray(resp, "ids")
	if err != nil {
		return nil, fmt.Errorf("failed list string array with error: %v", err)
	}

	return ids, nil
}
