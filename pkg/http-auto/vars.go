package auto

import (
	logging "github.com/ipfs/go-log/v2"
	basicHttp "github.com/taubyte/tau/pkg/http/basic"
)

var DefaultAllowedMethods = basicHttp.DefaultAllowedMethods
var logger = logging.Logger("tau.http.auto")
