package engine

import (
	"context"
	"strings"

	iface "github.com/taubyte/go-interfaces/services/tns"
	commonSpec "github.com/taubyte/go-specs/common"
)

func (e *Engine) Lookup(ctx context.Context, ops ...iface.Query) (keys []string, err error) {
	keys = make([]string, 0)
	var temp []string
	for _, q := range ops {
		if q.RegEx {
			temp, err = e.db.ListRegEx(ctx, "", keyFromPath(q.Prefix))
			if err != nil {
				return
			}
			keys = append(keys, temp...)
		} else if q.Prefix != nil {
			temp, err = e.db.List(ctx, keyFromPath(q.Prefix))
			if err != nil {
				return
			}
			keys = append(keys, temp...)
		}
	}
	returnKeys := make([]string, 0)
	for _, k := range keys {
		returnKeys = append(returnKeys, strings.TrimPrefix(k, commonSpec.TnsProtocol))
	}
	return returnKeys, nil
}
