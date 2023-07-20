package config

import (
	http "github.com/taubyte/http"
	"github.com/taubyte/p2p/peer"
)

type Protocols struct {
	Shape        string
	Node         peer.Node
	Http         http.Service
	ClientNode   peer.Node
	DVPrivateKey []byte
	DVPublicKey  []byte

	Odo
}
