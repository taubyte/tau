package function

import (
	"fmt"

	"github.com/taubyte/go-interfaces/services/substrate/components"
	"github.com/taubyte/go-interfaces/services/substrate/components/http"
	"github.com/taubyte/go-interfaces/services/tns"
	"github.com/taubyte/go-specs/extract"
	"github.com/taubyte/tau/protocols/substrate/components/http/common"
	"github.com/taubyte/tau/vm/cache"
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
	f.assetId, err = cache.ResolveAssetCid(f, f.branch)
	if err != nil {
		return nil, fmt.Errorf("getting asset id failed with: %w", err)
	}

	return f, nil
}
