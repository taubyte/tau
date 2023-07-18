package p2p

import (
	"fmt"

	moodyCommon "github.com/taubyte/go-interfaces/moody"
	"github.com/taubyte/go-interfaces/p2p/streams"
	"github.com/taubyte/utils/maps"
)

func (c *Client) Rare() ([]string, error) {
	// looks for items that only have one copy in the network
	resp, err := c.client.Send("hoarder", streams.Body{"action": "rare"})
	if err != nil {
		logger.Error(moodyCommon.Object{"message": fmt.Sprintf("Failed getting rare cids with error: %v", err)})
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
