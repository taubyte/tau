package http

import (
	"errors"
	"fmt"
	netUrl "net/url"
	"time"
)

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
		_, err := netUrl.ParseRequestURI(url)
		if err != nil {
			return fmt.Errorf("parsing url failed with: %w", err)
		}

		c.url = url
		return nil
	}
}

// Provider returns an Option that will set the git provider of the client
// currently only github is supported
func Provider(provider supportedProvider) Option {
	return func(c *Client) error {
		switch provider {
		case Github:
			c.provider = string(provider)
		case Bitbucket:
			return fmt.Errorf("provider %s currently is not supported", provider)
		default:
			return fmt.Errorf("provider %s unknown", provider)
		}

		return nil
	}
}

// Auth returns an Option that will set the auth token of the client
func Auth(token string) Option {
	return func(c *Client) error {
		if token == "" {
			return errors.New("cannot set empty token")
		}

		c.token = token
		return nil
	}
}

// Timeout returns an Option that will set the timeout of the client
func Timeout(duration time.Duration) Option {
	return func(c *Client) error {
		if duration == 0 {
			duration = DefaultTimeout
		}

		c.timeout = duration
		return nil
	}
}
