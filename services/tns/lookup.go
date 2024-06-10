package tns

import (
	"context"

	iface "github.com/taubyte/tau/core/services/tns"
	"github.com/taubyte/tau/p2p/streams"
	"github.com/taubyte/tau/p2p/streams/command"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
	"github.com/taubyte/utils/maps"
)

func (srv *Service) lookupHandler(ctx context.Context, conn streams.Connection, body command.Body) (cr.Response, error) {
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
