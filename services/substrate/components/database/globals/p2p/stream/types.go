package api

import (
	streams "github.com/taubyte/p2p/streams/service"
	"github.com/taubyte/tau/core/services/substrate/components/database"
)

type StreamHandler struct {
	srv    database.Service
	stream *streams.CommandService
}

func (s *StreamHandler) Close() {
	s.stream.Stop()
}
