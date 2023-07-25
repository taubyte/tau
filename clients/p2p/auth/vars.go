package auth

import "github.com/ipfs/go-log/v2"

var (
	MinPeers = 2
	MaxPeers = 4
	logger   = log.Logger("auth.api.p2p")
)
