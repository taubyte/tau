package client

import (
	"context"
	"net/http"
	"time"

	"bitbucket.org/taubyte/go-auth-http/git/common"
)

type Client struct {
	ctx         context.Context
	client      *http.Client
	gitClient   common.Client
	token       string
	provider    string
	url         string
	auth_header string
	unsecure    bool
	timeout     time.Duration
}

type User struct {
	client   *Client
	userData *UserData
}

type DomainResponse struct {
	Token string `json:"token"`
	Entry string `json:"entry"`
	Type  string `json:"type"`
}

type CanBeCreated interface {
	Create(c *Client) error
}
