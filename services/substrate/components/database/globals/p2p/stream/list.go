package api

import (
	"context"

	"github.com/taubyte/tau/p2p/streams"
	"github.com/taubyte/tau/p2p/streams/command"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
	"github.com/taubyte/utils/maps"
)

func (s *StreamHandler) listHandler(ctx context.Context, conn streams.Connection, body command.Body) (cr.Response, error) {
	projectID, err := maps.String(body, "projectID")
	if err != nil {
		return nil, err
	}

	prefix, err := maps.String(body, "prefix")
	if err != nil {
		return nil, err
	}

	db, err := s.srv.Global(projectID)
	if err != nil {
		return nil, err
	}

	keys, err := db.KV().List(ctx, prefix)
	if err != nil {
		return nil, err
	}

	return cr.Response{"keys": keys}, nil
}
