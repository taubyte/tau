package client

import (
	"errors"

	"github.com/taubyte/go-interfaces/p2p/streams"
)

func (c *Client) Get(projectID, key, _type string) (interface{}, error) {
	response, err := c.streamClient.Send("get", streams.Body{
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
