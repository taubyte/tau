package auto

import (
	logging "github.com/ipfs/go-log/v2"
	basicHttp "github.com/taubyte/http/basic"
)

var DefaultAllowedMethods = basicHttp.DefaultAllowedMethods
var logger = logging.Logger("tau.http.auto")
