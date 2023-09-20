package substrate

import (
	"github.com/taubyte/p2p/streams/client"
)

func (c *Client) ProxyHTTP(host, path, method string, ops ...client.Option) (<-chan *client.Response, error) {
	body := map[string]interface{}{
		BodyHost:   host,
		BodyPath:   path,
		BodyMethod: method,
	}

	ops = append(ops, c.options(body)...)
	return c.client.New(CommandHTTP, ops...).Do()
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
