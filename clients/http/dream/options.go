package http

import (
	"errors"
	"fmt"
	neturl "net/url"
	"time"
)

type Option func(c *Client) error

// does not check Certificate Chain
func Unsecure() Option {
	return func(c *Client) error {
		c.unsecure = true
		return nil
	}
}

func URL(url string) Option {
	return func(c *Client) error {
		_, err := neturl.ParseRequestURI(url)
		if err != nil {
			return fmt.Errorf("New client options: Parsing url failed with %s", err.Error())
		}
		c.url = url
		return nil
	}
}

func Provider(provider string) Option {
	var providers = map[string]bool{
		"github":    true,
		"bitbucket": false,
	}
	return func(c *Client) error {
		enabled, ok := providers[provider]
		if !ok {
			return fmt.Errorf("new client provider option `%s` unknown", provider)
		}
		if !enabled {
			return fmt.Errorf("new client provider option `%s` not enabled", provider)
		}
		c.provider = provider
		return nil
	}
}

func Auth(token string) Option {
	return func(c *Client) error {
		if token == "" {
			return errors.New("New client token option can not be empty")
		}
		c.token = token
		return nil
	}
}

func Timeout(duration time.Duration) Option {
	return func(c *Client) error {
		if duration < 1*time.Second {
			return errors.New("New client timeout option too low (<1s)")
		}
		c.timeout = duration
		return nil
	}
}
