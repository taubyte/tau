package common

import "github.com/ipfs/go-log/v2"

var (
	BroadcastInterval = 5
	Logger            = log.Logger("substrate.service.storage")
)

const (
	KvVersion = "v/"
	KvSize    = "s/"
)
