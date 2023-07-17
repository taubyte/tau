package api

import (
	streams "bitbucket.org/taubyte/p2p/streams/service"
	"github.com/taubyte/go-interfaces/services/substrate/database"
)

type StreamHandler struct {
	srv    database.Service
	stream *streams.CommandService
}

func (s *StreamHandler) Close() {
	s.stream.Stop()
}
