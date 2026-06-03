package website

import (
	"context"
	"sync"

	"github.com/spf13/afero"
	commonIface "github.com/taubyte/tau/core/services/substrate/components"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	websiteSpec "github.com/taubyte/tau/pkg/specs/website"
	"github.com/taubyte/tau/services/substrate/components/http/common"
	"github.com/taubyte/tau/services/substrate/components/metrics"
	"github.com/taubyte/tau/services/substrate/runtime"
)

type Website struct {
	srv commonIface.ServiceComponent

	config        structureSpec.Website
	computedPaths map[string][]string
	root          afero.Fs

	// assetFiles is the set of absolute paths (site-root relative) of every
	// regular file in the build asset, excluding the internal SSR directory.
	// It is used to decide whether a request resolves to a real static file.
	assetFiles map[string]struct{}

	// ssr is the parsed SSR manifest when the build asset carries one; nil for
	// classic static websites.
	ssr *websiteSpec.Manifest
	// ssrRuntime is the WebAssembly server bundle, instantiated lazily on the
	// first dynamic request and reused for the lifetime of the serviceable.
	ssrRuntime *runtime.Function
	// ssrHandlerData holds the server bundle bytes read out of the build asset
	// while it is open, so the runtime can be built lazily without re-fetching.
	ssrHandlerData []byte
	ssrHandlerCid  string
	ssrOnce        sync.Once
	ssrErr         error

	// ssrCID memoizes the DAG cid of the server bundle (added on first use),
	// shared by the function-ABI runtime and the WASI-stdio path.
	ssrCIDOnce sync.Once
	ssrCID     string
	ssrCIDErr  error

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
