package hoarder

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/ipfs/go-datastore"
	hoarderSpecs "github.com/taubyte/go-specs/hoarder"
	"github.com/taubyte/p2p/streams"
	"github.com/taubyte/p2p/streams/command"
	cr "github.com/taubyte/p2p/streams/command/response"
	"github.com/taubyte/utils/maps"
)

func (srv *Service) setupStreamRoutes() {
	srv.stream.Define("ping", func(context.Context, streams.Connection, command.Body) (cr.Response, error) {
		return cr.Response{"time": int(time.Now().Unix())}, nil
	})
	srv.stream.Define("hoarder", srv.ServiceHandler)
}

// TODO: This can be made generic
func (srv *Service) ServiceHandler(ctx context.Context, conn streams.Connection, body command.Body) (cr.Response, error) {
	action, err := maps.String(body, "action")
	if err != nil {
		return nil, fmt.Errorf("getting action failed with: %w", err)
	}

	cid, err := maps.String(body, "cid")
	if err != nil {
		cid = ""
	}

	switch action {
	case "stash":
		return srv.stashHandler(ctx, cid)
	case "rare":
		return srv.rareHandler(ctx)
	case "list":
		return srv.listHandler(ctx)
	}

	return nil, fmt.Errorf("action %s unknown", action)
}

func (srv *Service) listHandler(ctx context.Context) (cr.Response, error) {
	cids := make([]string, 0)
	entries, err := srv.db.List(ctx, hoarderSpecs.StashPath)
	if err != nil {
		return nil, fmt.Errorf("list failed with: %w", err)
	}

	for _, ids := range entries {
		allKeys := strings.Split(ids, "/")
		cids = append(cids, allKeys[len(allKeys)-1])
	}

	return cr.Response{"ids": cids}, nil
}

func (srv *Service) stashHandler(ctx context.Context, cid string) (cr.Response, error) {
	key := datastore.NewKey(hoarderSpecs.CreateStashPath(cid))
	if data, _ := srv.db.Get(ctx, key.String()); data == nil {
		logger.Infof("file with cid:%s not in database", cid)
		file, err := srv.node.GetFile(ctx, cid)
		if err != nil {
			return nil, fmt.Errorf("get failed with: %w", err)
		}

		if _, err = srv.node.AddFile(file); err != nil {
			return nil, fmt.Errorf("adding file to node failed with:: %w", err)
		}

		registryBytes, err := cbor.Marshal(&registryItem{Replicas: 1})
		if err != nil {
			return nil, fmt.Errorf("cbor marshal of registry failed with: %w", err)
		}

		srv.regLock.Lock()
		defer srv.regLock.Unlock()
		key := datastore.NewKey(hoarderSpecs.CreateStashPath(cid))
		if err = srv.db.Put(ctx, key.String(), registryBytes); err != nil {
			return nil, fmt.Errorf("put of registry in kvdb failed with: %w", err)
		}

		return cr.Response{"cid": cid}, nil
	}

	return nil, nil
}

func (srv *Service) rareHandler(ctx context.Context) (cr.Response, error) {
	var rareCID []string
	entries, err := srv.db.List(ctx, hoarderSpecs.StashPath)
	if err != nil {
		return nil, fmt.Errorf("list failed with: %w", err)
	}

	for _, id := range entries {
		var replicaData *registryItem
		respBytes, err := srv.db.Get(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("getting kvdb item failed with: %w", err)
		}

		if err = cbor.Unmarshal(respBytes, &replicaData); err != nil {
			return nil, fmt.Errorf("cbor unmarshal of replica failed with: %w", err)
		}

		id = strings.TrimPrefix(id, hoarderSpecs.StashPath)
		if replicaData.Replicas == 1 {
			rareCID = append(rareCID, id)
		}

	}

	return cr.Response{"rare": rareCID}, nil
}

func (s *Service) ping(context.Context, streams.Connection, command.Body) (cr.Response, error) {
	return cr.Response{"time": int(time.Now().Unix())}, nil
}
