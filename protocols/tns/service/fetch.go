package service

import (
	"context"

	cr "bitbucket.org/taubyte/p2p/streams/command/response"

	"github.com/taubyte/go-interfaces/p2p/streams"
	"github.com/taubyte/utils/maps"
	// TODO: use https://github.com/polydawn/refmt/cbor to minimize size (used by github.com/ipfs/go-ipld-cbor
)

func (srv *Service) fetchHandler(ctx context.Context, conn streams.Connection, body streams.Body) (cr.Response, error) {
	path, err := maps.StringArray(body, "path")
	if err != nil {
		return nil, err
	}
	_obj, err := srv.engine.Get(ctx, path...)
	if err != nil {
		return nil, err
	}
	return cr.Response{"object": _obj.Interface()}, nil
}
