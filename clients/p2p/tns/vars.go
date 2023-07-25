package tns

import (
	"time"

	"github.com/ipfs/go-log/v2"
)

var (
	MinPeers                  = 0
	MaxPeers                  = 4
	CacheFetchRetryWait       = 1 * time.Second
	MaximumCacheFetchInterval = 1 * time.Second
	logger                    = log.Logger("tns.api.p2p")
)
