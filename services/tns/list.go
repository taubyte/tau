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
	keys := make([]string, 0)
	unique := make(map[string]bool)
	depth, err := maps.Int(body, "depth")
	if err != nil {
		return nil, err
	}

	_keys, err := srv.engine.Lookup(ctx, iface.Query{Prefix: []string{}, RegEx: false})
	if err != nil {
		return nil, fmt.Errorf("failed list with error: %v", err)
	}

	for _, key := range _keys {
		_key := strings.Split(key, "/")
		if len(_key)-1 < depth {
			logger.Errorf("Depth of %d is longer than key: %s", depth, key)
			continue
		} else {
			if _, ok := unique[_key[depth]]; !ok {
				unique[_key[depth]] = true
				keys = append(keys, _key[depth])
			}
		}
	}

	return cr.Response{"keys": keys}, nil
}
