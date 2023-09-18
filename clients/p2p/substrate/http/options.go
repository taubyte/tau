package http

import "time"

func CallBack(callback func() Parameters) Option {
	return func(c *Client) error {
		c.callback = callback
		return nil
	}
}

func Timeout(timeout time.Duration) Option {
	return func(c *Client) error {
		c.defaults.Timeout = timeout
		return nil
	}
}

func Threshold(threshold int) Option {
	return func(c *Client) error {
		c.defaults.Threshold = threshold
		return nil
	}
}
