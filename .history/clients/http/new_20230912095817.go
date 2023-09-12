package http

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
)

// This function is used to return a pointer to a new HTTP client or returns an error.
func New(ctx context.Context, options ...Option) (*Client, error) {
	c := &Client{
		timeout:  DefaultTimeout,
		ctx:      ctx,
		provider: string(Github),
	}
	// For each of the options, apply them to the client or return an error.
	for _, opt := range options {
		if err := opt(c); err != nil {
			return nil, fmt.Errorf("client options failed with: %w", err)
		}
	}
	// Create a new HTTP client and specify the timeout.
	c.client = &http.Client{
		Timeout: c.timeout,
	}
	// If the client is unsecure, create a new HTTP client without TLS verification.
	if c.unsecure {
		c.client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		}
	} else { // If the client is secure, create a new HTTP client with TLS and specify the root CA.
		c.client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs: rootCAs,
			},
		}
	}
	// Set the authorization header for the HTTP client.
	c.auth_header = fmt.Sprintf("%s %s", c.provider, c.token)
	// Return the client pointer on success or nothing on error.
	return c, nil
}
