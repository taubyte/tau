//go:build wasmtime_component

package substrate

import (
	"github.com/taubyte/tau/services/substrate/components/http/website/componentbindings"

	// Registers the wasmtime/wasi-http ComponentRuntime so `component` ABI
	// website assets are served by the StarlingMonkey engine.
	_ "github.com/taubyte/tau/services/substrate/components/http/website/wasmtimehttp"
)

// attachComponentBindings starts the loopback binding server and wires each
// website's env.KV / env.STORAGE to this node's database + storage services
// (scoped to the website's project/application; the matcher defaults to the
// website name). Secrets are not wired yet — there is no built-in secret store
// (see componentbindings.Options.Secrets). The server is closed on shutdown.
func (srv *Service) attachComponentBindings() error {
	server, err := componentbindings.Enable(
		srv.components.database,
		srv.components.storage,
		componentbindings.Options{},
	)
	if err != nil {
		return err
	}
	srv.componentBindings = server
	return nil
}
