package ipfs

import (
	p2p "github.com/taubyte/go-interfaces/p2p/peer"
)

type Option func(*Service) error

type Service struct {
	p2p.Node
	private       bool
	swarmListen   []string
	swarmAnnounce []string
	privateKey    []byte
}

func (s *Service) Close() {
	s.Node.Close()
}
