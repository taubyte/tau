package api

import (
	"github.com/taubyte/tau/core/services/substrate/components/database"
	streams "github.com/taubyte/tau/p2p/streams/service"
)

type StreamHandler struct {
	srv    database.Service
	stream *streams.CommandService
}

func (s *StreamHandler) Close() {
	s.stream.Stop()
}
