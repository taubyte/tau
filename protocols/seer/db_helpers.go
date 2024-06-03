package seer

import (
	"context"
	"time"

	"github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/query"
	iface "github.com/taubyte/go-interfaces/services/seer"
	"github.com/taubyte/utils/maps"
)

func (h *dnsHandler) getServiceIp(ctx context.Context, proto string) ([]string, error) {
	result, err := h.seer.ds.Query(
		ctx, query.Query{
			Prefix: datastore.NewKey("/node/meta").ChildString(proto).String(),
		})
	if err != nil {
		return nil, err
	}

	unique := make(map[string]interface{}, 0)

	for entry := range result.Next() {
		key := datastore.NewKey(entry.Key)
		if key.Name() == "IP" {
			id := key.Path().Name()
			tsBytes, err := h.seer.ds.Get(ctx, datastore.NewKey("/hb/ts").Instance(id))
			if err != nil {
				continue
			}

			if bytesToInt64(tsBytes) >= time.Now().UnixNano()-ValidServiceResponseTime.Nanoseconds() {
				unique[string(entry.Value)] = nil
			}
		}
	}

	return maps.Keys(unique), nil
}

func (srv *oracleService) insertHandler(ctx context.Context, id string, services iface.Services) ([]string, error) {
	logger.Infof("Inserting protocols: %s, for id: %s", services, id)

	b, err := srv.ds.Batch(ctx)
	if err != nil {
		return nil, err
	}

	registered := make([]string, 0)
	for _, service := range services {
		proto := string(service.Type)

		b.Put(
			ctx,
			datastore.NewKey("/proto").Child(
				datastore.NewKey(proto).Instance(id),
			),
			int64ToBytes(time.Now().UnixNano()),
		)
		b.Put(
			ctx,
			datastore.NewKey("/node/proto").ChildString(id).Instance(proto),
			int64ToBytes(time.Now().UnixNano()),
		)

		registered = append(registered, proto)
		if service.Meta != nil {
			for key, value := range service.Meta {
				b.Put(
					ctx,
					datastore.NewKey("/node/meta").ChildString(proto).ChildString(id).Instance(key),
					[]byte(value),
				)
			}
		}
	}

	if err = b.Commit(ctx); err != nil {
		return nil, err
	}

	return registered, nil
}
