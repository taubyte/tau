package node

import (
	"time"

	"github.com/ipfs/go-log/v2"
)

var (
	WaitForSwamDuration = 10 * time.Second
	logger              = log.Logger("odo.cli.node")
)
