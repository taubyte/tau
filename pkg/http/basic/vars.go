package basic

import (
	"time"

	logging "github.com/ipfs/go-log/v2"
)

var (
	DefaultAllowedMethods = []string{"OPTIONS", "HEAD", "GET", "PUT", "POST", "DELETE", "PATCH"}
	ShutDownGrace         = 1 * time.Second
	logger                = logging.Logger("http.basic")
)
