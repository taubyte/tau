package monkey

import (
	"fmt"

	"github.com/taubyte/tau/p2p/streams/command"
	"github.com/taubyte/utils/maps"
)

// https://stackoverflow.com/questions/39391437/merge-two-or-more-mapstringinterface-types-into-one-in-golang
func mergeMaps(maps ...map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	for _, m := range maps {
		for k, v := range m {
			result[k] = v
		}
	}
	return result
}

func (c *Client) Update(jid string, body map[string]interface{}) (string, error) {
	// check this job again
	resp, err := c.client.Send("job", mergeMaps(command.Body{"action": "update", "jid": jid}, body), c.peers...)
	if err != nil {
		return jid, fmt.Errorf("failed calling send with error: %w", err)
	}
	if empty, exits := resp["update"]; empty == nil && exits {
		return jid, nil
	}
	jid, err = maps.String(resp, "update")
	if err != nil {
		return jid, fmt.Errorf("failed calling maps string array with error: %w", err)
	}
	return jid, nil
}
