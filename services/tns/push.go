package tns

import (
	"context"
	"errors"

	"github.com/taubyte/tau/p2p/streams/command"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
	"github.com/taubyte/tau/services/tns/flat"

	"github.com/taubyte/tau/p2p/streams"
	"github.com/taubyte/utils/maps"
)

func (srv *Service) pushHandler(ctx context.Context, conn streams.Connection, body command.Body) (cr.Response, error) {
	path, err := maps.StringArray(body, "path")
	if err != nil {
		return nil, err
	}

	_data, ok := body["data"]
	if !ok {
		return nil, errors.New("no data provided")
	}

	object, err := flat.New(path, _data)
	if err != nil {
		return nil, err
	}

	err = srv.engine.Merge(ctx, object)
	if err != nil {
		return nil, err
	}

	return cr.Response{"pushed": true}, nil
}
