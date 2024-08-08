package substrate

import (
	"github.com/taubyte/tau/p2p/streams/client"
)

func (c *Client) ProxyHTTP(host, path, method string, ops ...client.Option[client.Request]) (<-chan *client.Response, error) {
	body := map[string]interface{}{
		BodyHost:   host,
		BodyPath:   path,
		BodyMethod: method,
	}

	mainOptions := append(c.defaultOptions(), client.Body(body))

	return c.client.New(CommandHTTP, append(mainOptions, ops...)...).Do()
}

func (c *Client) defaultOptions() []client.Option[client.Request] {
	options := make([]client.Option[client.Request], 0, 10)
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

	if len(c.peers) > 0 {
		options = append(options, client.To(c.peers...))
	}

	return options
}

func (c *Client) Close() error {
	c.callback = nil
	c.client.Close()
	return nil
}
