package website

import (
	"context"

	"github.com/spf13/afero"
	commonIface "github.com/taubyte/go-interfaces/services/substrate/components"
	iface "github.com/taubyte/go-interfaces/services/substrate/components/http"
	structureSpec "github.com/taubyte/go-specs/structure"
	"github.com/taubyte/odo/protocols/node/components/http/common"
)

var _ commonIface.Serviceable = &Website{}
var _ iface.Serviceable = &Website{}
var _ iface.Website = &Website{}

type Website struct {
	srv iface.Service

	config        structureSpec.Website
	computedPaths map[string][]string
	root          afero.Fs
	fileId        string

	matcher     *common.MatchDefinition
	project     string
	application string
	branch      string
	commit      string

	ctx  context.Context
	ctxC context.CancelFunc

	readyCtx   context.Context
	readyCtxC  context.CancelFunc
	readyError error
	readyDone  bool

	instanceCtx  context.Context
	instanceCtxC context.CancelFunc
}

func (w *Website) Close() {
	w.instanceCtxC()
}
