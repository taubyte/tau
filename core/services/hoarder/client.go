package hoarder

import (
	"github.com/taubyte/tau/p2p/streams/command/response"
)

type Client interface {
	Rare() ([]string, error)
	Stash(cid string) (response.Response, error)
	List() ([]string, error)
}
