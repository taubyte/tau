package monkey

import (
	peerCore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/tau/p2p/streams/command/response"
)

type Client interface {
	Status(jid string) (*StatusResponse, error)
	Update(jid string, body map[string]interface{}) (string, error)
	List() ([]string, error)
	Cancel(jid string) (response.Response, error)
	Peers(...peerCore.ID) Client
	Close()
}
