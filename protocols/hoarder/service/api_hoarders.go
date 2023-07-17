package service

import (
	"context"
	"fmt"
	"strings"

	cr "bitbucket.org/taubyte/p2p/streams/command/response"
	"github.com/fxamacker/cbor/v2"
	"github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/query"
	"github.com/taubyte/go-interfaces/p2p/streams"
	hoarderSpecs "github.com/taubyte/go-specs/hoarder"
	"github.com/taubyte/utils/maps"
)

func (srv *Service) ServiceHandler(ctx context.Context, conn streams.Connection, body streams.Body) (cr.Response, error) {
	action, err := maps.String(body, "action")
	if err != nil {
		return nil, err
	}

	cid, err := maps.String(body, "cid")
	if err != nil {
		cid = ""
	}

	switch action {
	case "stash":
		return srv.stashHandler(ctx, cid)
	case "rare":
		return srv.rareHandler()
	case "list":
		return srv.listHandler()
	}

	return nil, fmt.Errorf("action %s unknown", action)
}

func (srv *Service) listHandler() (cr.Response, error) {
	cids := make([]string, 0)
	_result, err := srv.store.Query(srv.ctx, query.Query{Prefix: hoarderSpecs.StashPath})
	if err != nil {
		return nil, err
	}

	entries, err := _result.Rest()
	if err != nil {
		return nil, err
	}

	if len(entries) > 0 {
		for _, ids := range entries {
			allKeys := strings.Split(ids.Key, "/")
			cids = append(cids, allKeys[len(allKeys)-1])
		}
	}

	return cr.Response{"ids": cids}, nil
}

func (srv *Service) stashHandler(ctx context.Context, cid string) (cr.Response, error) {
	if exist, _ := srv.store.Has(srv.ctx, datastore.NewKey(hoarderSpecs.CreateStashPath(cid))); !exist {
		reader, err := srv.node.GetFile(ctx, cid)
		if err != nil {
			return nil, fmt.Errorf("failed calling get file with error: %w", err)
		}

		if _, err = srv.node.AddFile(reader); err != nil {
			return nil, fmt.Errorf("failed calling add file with error: %w", err)
		}

		registryBytes, err := cbor.Marshal(&registryItem{Replicas: 1})
		if err != nil {
			return nil, err
		}

		srv.regLock.Lock()
		defer srv.regLock.Unlock()
		if err = srv.store.Put(srv.ctx, datastore.NewKey(hoarderSpecs.CreateStashPath(cid)), registryBytes); err != nil {
			return nil, err
		}

		return cr.Response{"cid": cid}, nil
	}

	return nil, nil
}

func (srv *Service) rareHandler() (cr.Response, error) {
	var rareCID []string
	_result, err := srv.store.Query(srv.ctx, query.Query{Prefix: hoarderSpecs.StashPath})
	if err != nil {
		return nil, err
	}

	entries, err := _result.Rest()
	if err != nil {
		return nil, err
	}

	if len(entries) > 0 {
		for _, ids := range entries {
			var replicaData *registryItem
			respBytes, err := srv.store.Get(srv.ctx, datastore.NewKey(ids.Key))
			if err != nil {
				return nil, err
			}

			if err = cbor.Unmarshal(respBytes, &replicaData); err != nil {
				return nil, err
			}

			if replicaData.Replicas == 1 {
				rareCID = append(rareCID, ids.Key)
			}

		}
	}

	return cr.Response{"rare": rareCID}, nil
}
