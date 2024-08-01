package function

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

func New(srv components.ServiceComponent, object tns.Object, matcher *common.MatchDefinition) (http.Serviceable, error) {
	parser, err := extract.Tns().BasicPath(object.Path().String())
	if err != nil {
		return nil, fmt.Errorf("failed to parse tns path `%s` with: %s", object.Path().String(), err)
	}

	id := parser.Resource()
	f := &Function{
		srv:         srv,
		project:     parser.Project(),
		matcher:     matcher,
		application: parser.Application(),
		commit:      parser.Commit(),
		branch:      parser.Branch(),
	}

	if err = object.Bind(&f.config); err != nil {
		return nil, fmt.Errorf("failed to decode config with: %s", err)
	}

	f.config.Id = id
	if f.config.Source == "." { //TODO: eveywhere
		f.assetId, err = cache.ResolveAssetCid(f)
		if err != nil {
			return nil, fmt.Errorf("getting asset id failed with: %w", err)
		}
	}

	//TODO: account for library! better is moved to Runtime creation anyways
	assetCid, _ := cid.Decode(f.assetId)
	if exists, _ := srv.Node().DAG().HasBlock(srv.Context(), assetCid); exists {
		f.metrics.Cached += 0.3
	}

	return f, nil
}
