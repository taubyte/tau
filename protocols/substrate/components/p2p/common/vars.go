package common

import "github.com/ipfs/go-log/v2"

//TODO: Move to specs

const (
	ServiceName = "substrate_p2p"
	Protocol    = "/substrate/p2p/v1"
	MinPeers    = 0
	MaxPeers    = 4
)

var Logger = log.Logger("substrate.service.p2p")
