package ipfs

import "github.com/taubyte/p2p/peer"

type Option func(*Service) error

type Service struct {
	*peer.Node
	private       bool
	swarmListen   []string
	swarmAnnounce []string
	privateKey    []byte
}

func (s *Service) Close() {
	s.Node.Close()
}
