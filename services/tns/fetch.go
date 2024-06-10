package tns

import (
	"context"

	"github.com/taubyte/tau/p2p/streams/command"
	cr "github.com/taubyte/tau/p2p/streams/command/response"

	"github.com/taubyte/tau/p2p/streams"
	"github.com/taubyte/utils/maps"
	// TODO: use https://github.com/polydawn/refmt/cbor to minimize size (used by github.com/ipfs/go-ipld-cbor
)

func (srv *Service) fetchHandler(ctx context.Context, conn streams.Connection, body command.Body) (cr.Response, error) {
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
