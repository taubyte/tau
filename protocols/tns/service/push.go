package service

import (
	"context"
	"errors"

	cr "bitbucket.org/taubyte/p2p/streams/command/response"
	"github.com/taubyte/odo/protocols/tns/flat"

	"github.com/taubyte/go-interfaces/p2p/streams"
	"github.com/taubyte/utils/maps"
)

func (srv *Service) pushHandler(ctx context.Context, conn streams.Connection, body streams.Body) (cr.Response, error) {

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
