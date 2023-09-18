package hoarder

import (
	"time"

	"github.com/ipfs/go-log/v2"
)

const maxWaitTime = 15 * time.Second

var (
	logger              = log.Logger("hoarder.service")
	RebroadCastInterval = 5
)
