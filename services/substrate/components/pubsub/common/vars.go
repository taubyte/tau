package common

import "github.com/ipfs/go-log/v2"

var (
	WebSocketHttpPath = "/ws-{hash}/{channel:.+}"
	Logger            = log.Logger("tau.substrate.service.pubsub")
)
