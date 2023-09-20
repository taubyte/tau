package http

import (
	"fmt"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/p2p/streams/client"
)

func (c *Client) ProxyStreams(host, path, method string) (map[peer.ID]*client.Response, map[peer.ID]error, error) {
	body := map[string]interface{}{
		"host":   host,
		"path":   path,
		"method": method,
	}

	resCh, err := c.client.New("upgrade", c.options(body)...).Do()
	if err != nil {
		return nil, nil, fmt.Errorf("sending `upgrade` command failed with: %w", err)
	}

	responses := make(map[peer.ID]*client.Response)
	errors := make(map[peer.ID]error)
	ok := true
	var res *client.Response
	for ok {
		if res, ok = <-resCh; ok {
			pid := res.PID()
			if err := res.Error(); err != nil {
				errors[pid] = err
			} else {
				responses[pid] = res
			}
		}
	}

	return responses, errors, nil
}

func (c *Client) options(body map[string]interface{}) []client.Option {
	options := []client.Option{client.Body(body)}
	params := c.defaults
	if c.callback != nil {
		params = c.callback()
	}

	if params.Timeout > 0 {
		options = append(options, client.Timeout(params.Timeout))
	}

	if params.Threshold > 0 {
		options = append(options, client.Threshold(params.Threshold))
	}

	return options
}
