package api

import (
	"github.com/taubyte/go-interfaces/services/substrate/database"
	streams "github.com/taubyte/p2p/streams/service"
)

type StreamHandler struct {
	srv    database.Service
	stream *streams.CommandService
}

func (s *StreamHandler) Close() {
	s.stream.Stop()
}
