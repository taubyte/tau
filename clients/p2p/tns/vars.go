package p2p

import (
	"time"

	logging "github.com/ipfs/go-log/v2"
)

var (
	MinPeers                  = 0
	MaxPeers                  = 4
	CacheFetchRetryWait       = 1 * time.Second
	MaximumCacheFetchInterval = 1 * time.Second
	logger                    = logging.Logger("tns.api.p2p")
)
