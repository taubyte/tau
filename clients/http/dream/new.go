package http

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
)

func New(ctx context.Context, options ...Option) (*Client, error) {
	c := &Client{
		timeout:  DefaultTimeout,
		ctx:      ctx,
		unsecure: false,
	}

	for _, opt := range options {
		err := opt(c)
		if err != nil {
			return nil, fmt.Errorf("parsing options failed with: %w", err)
		}
	}

	c.client = &http.Client{
		Timeout: c.timeout,
	}

	if !c.unsecure {
		c.client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs: rootCAs,
			},
		}
	} else {
		c.client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		}
	}

	c.auth_header = fmt.Sprintf("%s %s", c.provider, c.token)

	return c, nil
}
