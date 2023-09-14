package tns

import (
	"time"

	"github.com/ipfs/go-log/v2"
)

var (
	CacheFetchRetryWait       = 1 * time.Second
	MaximumCacheFetchInterval = 1 * time.Second
	logger                    = log.Logger("tns.api.p2p")
)
