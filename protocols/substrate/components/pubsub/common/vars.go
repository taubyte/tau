package common

import "github.com/ipfs/go-log/v2"

var (
	WebSocketFormat   = "ws-%s/%s"
	WebSocketHttpPath = "/ws-{hash}/{channel:.+}"
	Logger            = log.Logger("substrate.service.pubsub")
)

const ReplaceMe = ""
