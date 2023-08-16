package client

import (
	httpClient "github.com/taubyte/tau/clients/http"
	"github.com/taubyte/tau/clients/http/auth/git/common"
)

type Client struct {
	http      *httpClient.Client
	gitClient common.Client
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
