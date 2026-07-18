package schema

import (
	"github.com/taubyte/tau/pkg/specs/common"
	"github.com/taubyte/tau/pkg/specs/methods"
	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v1/driver"
)

// Object-addressing capabilities: the set of TNS-key methods a compiled
// resource-object exposes (see pkg/specs/<res>). Each is a *driver.Cap carrying
// its own runtime path funcs, bound DIRECTLY to the pkg/specs/methods helper
// whose signature matches — so the driver reads the behaviour off the term
// instead of a term-keyed switch. Attached to the object template via
// engine.Addressing(...) on DefineIter; the index roles (ByName/ForeignKey/
// ScopePath) are consumed by driver.IndexByName/IndexForeignKey/IndexByScope.
//
// The engine only requires String(); a capability with no index role (basic/
// index/services/nameIndex/empty) carries just its Name.
var (
	HasBasicPath = &driver.Cap{Name: "basic"}     // BasicPath(branch,commit,project,app,id)
	HasIndex     = &driver.Cap{Name: "index"}     // IndexValue(branch,project,app,id)
	HasIndexPath = &driver.Cap{Name: "indexPath", // IndexPath(project,app)
		ByName: func(project, app, name string, _ common.PathVariable) (*common.TnsPath, error) {
			return methods.IndexPath(project, app, name), nil
		}}
	HasHttp       = &driver.Cap{Name: "http", ForeignKey: methods.HttpPath}              // HttpPath(fqdn)
	HasWasmModule = &driver.Cap{Name: "wasm", ByName: methods.WasmModulePath}            // WasmModulePath(...) + ModuleName(name)
	HasServices   = &driver.Cap{Name: "services"}                                        // ServicesPath(project,app,serviceId,command)
	HasWebSocket  = &driver.Cap{Name: "websocket", ScopePath: methods.WebSocketHashPath} // WebSocketPath / WebSocketHashPath
	HasNameIndex  = &driver.Cap{Name: "nameIndex"}                                       // NameIndex(...) (bespoke)
	HasEmptyPath  = &driver.Cap{Name: "empty"}                                           // EmptyPath()
)

// compile-time check that the tags satisfy the engine interface.
var _ engine.Capability = HasBasicPath
