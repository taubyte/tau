package node

import (
	moody "bitbucket.org/taubyte/go-moody-blues"
	"time"
)

var (
	logger, _           = moody.New("odo")
	WaitForSwamDuration = 10 * time.Second
)
