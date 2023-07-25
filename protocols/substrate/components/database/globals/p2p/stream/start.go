package api

import (
	"fmt"

	"github.com/taubyte/go-interfaces/services/substrate/components/database"
	"github.com/taubyte/odo/protocols/substrate/components/database/globals/p2p/common"
	streams "github.com/taubyte/p2p/streams/service"
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
