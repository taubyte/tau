package substrate

import "time"

var (
	DefaultTimeOut   = 10 * time.Millisecond
	DefaultThreshold = 5
)

const (
	Command = "proxy"

	ProxyHTTP   = "http"
	ProxyP2P    = "p2p"
	ProxyPubsub = "pubsub"

	BodyType   = "type"
	BodyHost   = "host"
	BodyPath   = "path"
	BodyMethod = "method"

	ResponseCached = "cached"
)
