package website

import (
	"context"

	"github.com/spf13/afero"
	commonIface "github.com/taubyte/tau/core/services/substrate/components"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/services/substrate/components/http/common"
	"github.com/taubyte/tau/services/substrate/components/metrics"
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
