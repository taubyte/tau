package http

import (
	"context"
	"net/http"
	"time"
)

type Client struct {
	client      *http.Client
	token       string
	provider    string
	url         string
	auth_header string
	unsecure    bool
	timeout     time.Duration
	ctx         context.Context
}

type Option func(c *Client) error

type supportedProvider string
