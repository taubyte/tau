package website

import (
	"fmt"

	"github.com/taubyte/go-interfaces/services/substrate/components"
	"github.com/taubyte/go-interfaces/services/substrate/components/http"
	"github.com/taubyte/go-interfaces/services/tns"
	"github.com/taubyte/go-specs/extract"
	"github.com/taubyte/tau/protocols/substrate/components/http/common"
	"github.com/taubyte/tau/vm/cache"
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

	w.assetId, err = cache.ResolveAssetCid(w, w.branch)
	if err != nil {
		return nil, fmt.Errorf("getting website asset id failed with: %w", err)
	}

	return w, nil
}
