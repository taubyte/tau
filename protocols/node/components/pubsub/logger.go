package pubsub

import (
	logging "github.com/ipfs/go-log/v2"
)

var subLogger logging.StandardLogger

func (s *Service) Logger() logging.StandardLogger {
	if subLogger == nil {
		subLogger = logging.Logger("node.pubsub")
	}

	return subLogger
}
