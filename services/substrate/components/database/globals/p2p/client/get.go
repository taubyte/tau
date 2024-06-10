package client

import (
	"errors"

	"github.com/taubyte/tau/p2p/streams/command"
)

func (c *Client) Get(projectID, key, _type string) (interface{}, error) {
	response, err := c.streamClient.Send("get", command.Body{
		"projectID": projectID,
		"key":       key,
		"type":      _type,
	})
	if err != nil {
		return nil, err
	}

	value, ok := response["value"]
	if !ok {
		return nil, errors.New("response missing value")
	}

	return value, nil
}
