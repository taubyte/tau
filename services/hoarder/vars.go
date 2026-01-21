package hoarder

import (
	"time"

	"github.com/ipfs/go-log/v2"
)

const maxWaitTime = 5 * time.Second

var (
	logger = log.Logger("tau.hoarder.service")
)
