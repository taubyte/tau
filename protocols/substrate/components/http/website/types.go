package website

import (
	"context"

	"github.com/spf13/afero"
	commonIface "github.com/taubyte/go-interfaces/services/substrate/components"
	structureSpec "github.com/taubyte/go-specs/structure"
	"github.com/taubyte/tau/protocols/substrate/components/http/common"
	"github.com/taubyte/tau/protocols/substrate/components/metrics"
)

type Website struct {
	srv commonIface.ServiceComponent

	config        structureSpec.Website
	computedPaths map[string][]string
	root          afero.Fs

	matcher     *common.MatchDefinition
	project     string
	application string
	branch      string
	commit      string

	assetId string

	readyCtx   context.Context
	readyCtxC  context.CancelFunc
	readyError error
	readyDone  bool

	provisioned bool
	metrics     metrics.Website

	instanceCtx  context.Context
	instanceCtxC context.CancelFunc
}
