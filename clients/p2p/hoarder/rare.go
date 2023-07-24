package p2p

import (
	"fmt"

	"github.com/taubyte/p2p/streams/command"
	"github.com/taubyte/utils/maps"
)

func (c *Client) Rare() ([]string, error) {
	// looks for items that only have one copy in the network
	resp, err := c.client.Send("hoarder", command.Body{"action": "rare"})
	if err != nil {
		logger.Errorf("Failed getting rare cids with error: %w", err)
		return nil, fmt.Errorf("failed calling send with error: %w", err)
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
