package uri

import (
	"io"
	"net/http"
	"os"
	"strings"
)

/* original: https://github.com/utahta/go-openuri */

// Client wraps extended HTTP client helpers.
type Client struct {
	httpClient *http.Client
}

// ClientOption type
type ClientOption func(*Client) error

// New returns a Client
func New(options ...ClientOption) (*Client, error) {
	c := &Client{httpClient: http.DefaultClient}
	for _, option := range options {
		if err := option(c); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// Open opens an io.ReadCloser from a local file or URL
func Open(name string, options ...ClientOption) (io.ReadCloser, error) {
	c, err := New(options...)
	if err != nil {
		return nil, err
	}
	return c.Open(name)
}

// WithHttpClient returns a ClientOption that sets the http.Client
func WithHTTPClient(v *http.Client) ClientOption {
	return func(c *Client) error {
		c.httpClient = v
		return nil
	}
}

// Open opens either a response body from a given url, or a file from a given path.
func (c *Client) Open(name string) (io.ReadCloser, error) {
	if strings.HasPrefix(name, "http://") || strings.HasPrefix(name, "https://") {
		resp, err := c.httpClient.Get(name)
		if err != nil {
			return nil, err
		}
		return resp.Body, nil
	}
	return os.Open(name)
}
