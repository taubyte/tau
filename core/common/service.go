package common

import peer "github.com/taubyte/tau/p2p/peer"

type Service interface {
	Node() peer.Node
	Close() error
}
