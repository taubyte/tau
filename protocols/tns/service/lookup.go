package service

import (
	"context"

	cr "bitbucket.org/taubyte/p2p/streams/command/response"
	"github.com/taubyte/go-interfaces/p2p/streams"
	iface "github.com/taubyte/go-interfaces/services/tns"
	"github.com/taubyte/utils/maps"
)

func (srv *Service) lookupHandler(ctx context.Context, conn streams.Connection, body streams.Body) (cr.Response, error) {
	prefixes, err := maps.StringArray(body, "prefix")
	if err != nil {
		return nil, err
	}

	regex, err := maps.Bool(body, "regex")
	if err != nil {
		return nil, err
	}

	keys, err := srv.engine.Lookup(ctx, iface.Query{Prefix: prefixes, RegEx: regex})
	if err != nil {
		return nil, err
	}

	return cr.Response{"keys": keys}, nil
}
