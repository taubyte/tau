package substrate

import "time"

var (
	DefaultTimeOut   = 10 * time.Millisecond
	DefaultThreshold = 3
)

const (
	CommandHTTP = "proxy-http"

	BodyHost   = "host"
	BodyPath   = "path"
	BodyMethod = "method"
)
