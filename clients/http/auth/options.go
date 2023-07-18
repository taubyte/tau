package client

import (
	"errors"
	"fmt"
	neturl "net/url"
	"time"
)

type Option func(c *Client) error

// Unsecure returns an Option that will allow the client to connect to a server with an invalid certificate
func Unsecure() Option {
	return func(c *Client) error {
		c.unsecure = true
		return nil
	}
}

// URL returns an Option that will set the url of the auth server
func URL(url string) Option {
	return func(c *Client) error {
		_, err := neturl.ParseRequestURI(url)
		if err != nil {
			return fmt.Errorf("new client options: Parsing url failed with %s", err.Error())
		}

		c.url = url
		return nil
	}
}

// Provider returns an Option that will set the git provider of the client
// currently only github is supported
func Provider(provider string) Option {
	var providers = map[string]bool{
		"github":    true,
		"bitbucket": false,
	}
	return func(c *Client) error {
		enabled, ok := providers[provider]
		if ok == false {
			return fmt.Errorf("new client provider option `%s` unknown", provider)
		}

		if enabled == false {
			return fmt.Errorf("new client provider option `%s` not enabled", provider)
		}

		c.provider = provider
		return nil
	}
}

// Auth returns an Option that will set the auth token of the client
func Auth(token string) Option {
	return func(c *Client) error {
		if token == "" {
			return errors.New("new client token option can not be empty")
		}

		c.token = token
		return nil
	}
}

// Timeout returns an Option that will set the timeout of the client
func Timeout(duration time.Duration) Option {
	return func(c *Client) error {
		if duration < 1*time.Second {
			return errors.New("new client timeout option too low (<1s)")
		}

		c.timeout = duration
		return nil
	}
}
