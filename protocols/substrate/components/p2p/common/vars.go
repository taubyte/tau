package common

import "github.com/ipfs/go-log/v2"

//TODO: Move to specs

const (
	ServiceName = "substrate"
	Protocol    = "/substrate/v1"
)

var Logger = log.Logger("substrate.service.p2p")
