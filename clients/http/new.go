package http

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
)

// New creates a new HTTP client with the provided options.
func New(ctx context.Context, options ...Option) (*Client, error) {
	// Create a new client with default values
	c := &Client{
		timeout:  DefaultTimeout,
		ctx:      ctx,
		provider: string(Github),
	}

	// Apply the provided options to the client
	for _, opt := range options {
		if err := opt(c); err != nil {
			return nil, fmt.Errorf("client options failed with: %w", err)
		}
	}

	// Create an HTTP client with the configured timeout
	c.client = &http.Client{
		Timeout: c.timeout,
	}

	// Configure the transport layer based on the secure/unsecure flag
	if c.unsecure {
		// If unsecure, skip TLS verification
		c.client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		}
	} else {
		// If secure, use the provided root CAs for TLS verification
		c.client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs: rootCAs,
			},
		}
	}
	// Set the authentication header using the provider and token
	c.auth_header = fmt.Sprintf("%s %s", c.provider, c.token)

	return c, nil
}
