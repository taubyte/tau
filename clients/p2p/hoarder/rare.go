package hoarder

import (
	"fmt"

	"github.com/taubyte/tau/p2p/streams/command"
	"github.com/taubyte/utils/maps"
)

// TODO: Add cids to dht
func (c *Client) Rare() ([]string, error) {
	// looks for items that only have one copy in the network
	resp, err := c.Send("hoarder", command.Body{"action": "rare"}, c.peers...)
	if err != nil {
		logger.Error("Failed getting rare cids with:", err.Error())
		return nil, fmt.Errorf("failed calling send with: %w", err)
	}

	if empty, exits := resp["rare"]; empty == nil && exits {
		return nil, nil
	}

	cids, err := maps.StringArray(resp, "rare")
	if err != nil {
		return nil, fmt.Errorf("failed calling maps string array with error: %w", err)
	}

	// return the array of string containing the items cid
	return cids, nil
}
