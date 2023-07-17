package p2p

import (
	"time"

	moody "bitbucket.org/taubyte/go-moody-blues"
)

var (
	MinPeers                  = 0
	MaxPeers                  = 4
	CacheFetchRetryWait       = 1 * time.Second
	MaximumCacheFetchInterval = 1 * time.Second
	logger, _                 = moody.New("tns.api.p2p")
)
