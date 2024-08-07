package website

import (
	"fmt"

	"github.com/ipfs/go-cid"
	"github.com/taubyte/tau/core/services/substrate/components"
	"github.com/taubyte/tau/core/services/substrate/components/http"
	"github.com/taubyte/tau/core/services/tns"
	"github.com/taubyte/tau/pkg/specs/extract"
	"github.com/taubyte/tau/services/substrate/components/http/common"
	"github.com/taubyte/tau/services/substrate/runtime/cache"
)

func New(srv components.ServiceComponent, object tns.Object, matcher *common.MatchDefinition) (serviceable http.Serviceable, err error) {
	parser, err := extract.Tns().BasicPath(object.Path().String())
	if err != nil {
		return nil, fmt.Errorf("failed to parse tns path `%s` with: %w", object.Path().String(), err)
	}

	id := parser.Resource()
	w := &Website{
		srv:           srv,
		project:       parser.Project(),
		branch:        parser.Branch(),
		application:   parser.Application(),
		matcher:       matcher,
		commit:        parser.Commit(),
		computedPaths: make(map[string][]string, 0),
	}

	if err = object.Bind(&w.config); err != nil {
		return nil, fmt.Errorf("failed to decode config with: %w", err)
	}
	w.config.Id = id

	w.assetId, err = cache.ResolveAssetCid(w)
	if err != nil {
		return nil, fmt.Errorf("getting website asset id failed with: %w", err)
	}

	assetCid, _ := cid.Decode(w.assetId)
	if exists, _ := srv.Node().DAG().HasBlock(srv.Context(), assetCid); exists {
		w.metrics.Cached += 0.3
	}

	return w, nil
}
