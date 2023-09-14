package substrate

import (
	"fmt"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/p2p/streams/client"
)

func (c *Client) Client() *client.Client {
	return c.client
}

func (c *Client) Has(host, path, method string, threshold int) (map[peer.ID]*client.Response, map[peer.ID]error, error) {
	body := map[string]interface{}{
		"host":   host,
		"path":   path,
		"method": method,
	}

	resCh, err := c.client.New("has", client.Body(body), client.Threshold(threshold)).Do()
	if err != nil {
		return nil, nil, fmt.Errorf("sending `has` command failed with: %w", err)
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

func (c *Client) Upgrade(pid peer.ID) {}

func (c *Client) Tunnel(pid peer.ID) (*client.Response, error) {
	resCh, err := c.client.New("tunnel", client.To(pid)).Do()
	if err != nil {
		return nil, err
	}

	res := <-resCh
	if err := res.Error(); err != nil {
		return nil, err
	}

	return res, nil
}
