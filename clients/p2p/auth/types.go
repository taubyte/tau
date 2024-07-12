package auth

import (
	peerCore "github.com/libp2p/go-libp2p/core/peer"
	iface "github.com/taubyte/tau/core/services/auth"
	client "github.com/taubyte/tau/p2p/streams/client"
)

var _ iface.Client = &Client{}

type Client struct {
	client *client.Client
	peers  []peerCore.ID
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
