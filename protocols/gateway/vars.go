package gateway

import "time"

var (
	ChannelTimeout time.Duration = 100 * time.Millisecond
	ProxyHeader                  = "X-Substrate-Peer"
)
