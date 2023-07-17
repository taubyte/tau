package p2p

import (
	"fmt"

	"github.com/taubyte/go-interfaces/p2p/streams"
	"github.com/taubyte/go-interfaces/services/substrate/counters"
)

func (c *Client) Report(report map[string]counters.Metric) error {
	_, err := c.client.Send("counters", streams.Body{"action": "stash", "data": report})
	if err != nil {
		return fmt.Errorf("failed calling report with error: %w", err)
	}

	return nil
}
