package auth

import (
	client "github.com/taubyte/p2p/streams/client"
	iface "github.com/taubyte/tau/core/services/auth"
)

var _ iface.Client = &Client{}

type Client struct {
	client *client.Client
}

type Stats Client

type Hooks Client

type Projects Client

type Repositories Client
type GithubRepositories Repositories

type RepositoryCommon struct {
	project string
	Name    string
	Url     string
	id      int
}

type GithubRepository struct {
	RepositoryCommon
	Key string
}
