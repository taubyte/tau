package api

import (
	"fmt"

	streams "github.com/taubyte/p2p/streams/service"
	"github.com/taubyte/tau/core/services/substrate/components/database"
	"github.com/taubyte/tau/services/substrate/components/database/globals/p2p/common"
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
