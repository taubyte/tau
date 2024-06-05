package http

import (
	"context"
	"net/http"
	"time"

	"github.com/taubyte/tau/dream"
)

var Dev bool

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

func (c *Client) Universe(name string) *Universe {
	return &Universe{Name: name, client: c}
}

func (c *Client) StartUniverseWithConfig(name string, config *dream.Config) error {
	return c.post("/universe/"+name, map[string]interface{}{"config": config}, nil)
}

type Universe struct {
	Name   string
	client *Client
}
