package gateway

import "time"

var (
	ChannelTimeout time.Duration = 100 * time.Millisecond
	ProxyHeader                  = "X-Substrate-Peer"
	MaxScore                     = 50
)

// maxTries is how many nodes a request may be offered to. The second is only
// ever used after the first refused it before running it.
const maxTries = 2
