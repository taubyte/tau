package http

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
)

// This function is used to create a new HTTP client or returns an error.
func New(ctx context.Context, options ...Option) (*Client, error) {
	c := &Client{
		timeout:  DefaultTimeout,
		ctx:      ctx,
		provider: string(Github),
	}

	for _, opt := range options {
		if err := opt(c); err != nil {
			return nil, fmt.Errorf("client options failed with: %w", err)
		}
	}

	c.client = &http.Client{
		Timeout: c.timeout,
	}

	if c.unsecure {
		c.client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		}
	} else {
		c.client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs: rootCAs,
			},
		}
	}

	c.auth_header = fmt.Sprintf("%s %s", c.provider, c.token)

	return c, nil
}
