package substrate

import "time"

var (
	DefaultTimeOut   = 10 * time.Millisecond
	DefaultThreshold = 5
)

const (
	CommandHTTP = "proxy-http"

	BodyHost   = "host"
	BodyPath   = "path"
	BodyMethod = "method"

	ResponseCached = "cached"
)
