package hoarder

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/taubyte/tau/p2p/streams"
	"github.com/taubyte/tau/p2p/streams/command"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
	hoarderSpecs "github.com/taubyte/tau/pkg/specs/hoarder"
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
		peers, _ := maps.StringArray(body, "peers")
		return srv.stashHandler(ctx, cid, peers)
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

func (srv *Service) stashHandler(ctx context.Context, id string, peers []string) (cr.Response, error) {
	key := hoarderSpecs.CreateStashPath(id)
	if data, _ := srv.db.Get(ctx, key); data == nil {
		registryBytes, err := cbor.Marshal(&registryItem{Replicas: 0})
		if err != nil {
			logger.Errorf("cbor marshal of registry failed with: %w", err)
			return nil, err
		}

		if err = srv.db.Put(srv.node.Context(), key, registryBytes); err != nil {
			logger.Errorf("put of registry in kvdb failed with: %w", err)
			return nil, err
		}

		//TODO: start worker that does the stash
		// go func() {
		// 	logger.Infof("file with cid:%s not in database", id)

		// 	_cid, err := cid.Decode(id)
		// 	if err != nil {
		// 		logger.Errorf("failed parsing cid with: %w", err)
		// 		return
		// 	}

		// 	var n format.Node
		// 	tctx, tctxC := context.WithTimeout(srv.node.Context(), 10*time.Minute)
		// 	for {
		// 		_ctx, _ctxC := context.WithTimeout(tctx, 30*time.Second)
		// 		n, err = srv.node.DAG().Get(_ctx, _cid)
		// 		_ctxC()
		// 		if err == nil {
		// 			break
		// 		}
		// 	}
		// 	tctxC()
		// 	if err != nil {
		// 		logger.Errorf("failed to fetch cid: %w", err)
		// 		return
		// 	}

		// 	file, err := ufsio.NewDagReader(srv.node.Context(), n, srv.node.DAG())

		// 	if err != nil {
		// 		logger.Errorf("get failed with: %w", err)
		// 		return
		// 	}

		// 	if _, err = srv.node.AddFile(file); err != nil {
		// 		logger.Errorf("adding file to node failed with:: %w", err)
		// 		return
		// 	}

		// 	registryBytes, err := cbor.Marshal(&registryItem{Replicas: 1})
		// 	if err != nil {
		// 		logger.Errorf("cbor marshal of registry failed with: %w", err)
		// 		return
		// 	}

		// 	key := datastore.NewKey(hoarderSpecs.CreateStashPath(id))
		// 	if err = srv.db.Put(srv.node.Context(), key.String(), registryBytes); err != nil {
		// 		logger.Errorf("put of registry in kvdb failed with: %w", err)
		// 		return
		// 	}
		// }()

		return cr.Response{"cid": id}, nil
	}

	return nil, nil
}

func (srv *Service) rareHandler(ctx context.Context) (cr.Response, error) {
	entries, err := srv.db.List(ctx, hoarderSpecs.StashPath)
	if err != nil {
		return nil, fmt.Errorf("list failed with: %w", err)
	}

	rareCID := make([]string, 0)
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
		if replicaData.Replicas <= 1 {
			rareCID = append(rareCID, id)
		}

	}

	return cr.Response{"rare": rareCID}, nil
}
