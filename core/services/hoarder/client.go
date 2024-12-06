package hoarder

import (
	peerCore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/tau/p2p/streams/command/response"
)

type Client interface {
	Rare() ([]string, error)
	Stash(cid string, peers ...string) (response.Response, error)
	List() ([]string, error)
	Peers(...peerCore.ID) Client
	Close()
}
