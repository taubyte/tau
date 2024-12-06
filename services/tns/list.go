package tns

import (
	"context"
	"fmt"
	"strings"

	iface "github.com/taubyte/tau/core/services/tns"
	"github.com/taubyte/tau/p2p/streams"
	"github.com/taubyte/tau/p2p/streams/command"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
	"github.com/taubyte/utils/maps"
)

func (srv *Service) listHandler(ctx context.Context, conn streams.Connection, body command.Body) (cr.Response, error) {
	depth, err := maps.Int(body, "depth")
	if err != nil {
		return nil, err
	}

	_keys, err := srv.engine.Lookup(ctx, iface.Query{Prefix: []string{}, RegEx: false})
	if err != nil {
		return nil, fmt.Errorf("failed list with error: %v", err)
	}

	uniq := make(map[string][]string)
	for _, key := range _keys {
		_key := strings.Split(key, "/")[1:]

		d := depth
		if d > len(_key) {
			d = len(_key)
		}
		for i := 1; i <= d; i++ {
			k := _key[:i]
			uniq[strings.Join(k, "/")] = k
		}
	}

	keys := make([][]string, 0, len(uniq))
	for _, v := range uniq {
		keys = append(keys, v)
	}

	return cr.Response{"keys": keys}, nil
}
