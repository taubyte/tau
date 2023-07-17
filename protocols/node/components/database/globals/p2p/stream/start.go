package api

import (
	"fmt"

	streams "bitbucket.org/taubyte/p2p/streams/service"
	"github.com/taubyte/go-interfaces/services/substrate/database"
	"github.com/taubyte/odo/protocols/node/components/database/globals/p2p/common"
)

func Start(srv database.Service) (streamHandler *StreamHandler, err error) {
	streamHandler = &StreamHandler{
		srv: srv,
	}

	if streamHandler.stream, err = streams.New(srv.Node(), common.StreamName, common.StreamProtocol); err != nil {
		return nil, fmt.Errorf("creating new stream failed with: %w", err)
	}

	streamHandler.setupRoutes()

	return
}
