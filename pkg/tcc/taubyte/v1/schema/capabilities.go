package schema

import "github.com/taubyte/tau/pkg/tcc/engine"

// capability is a typed object-addressing tag implementing engine.Capability.
// Using a named type (not a bare string) keeps Addressing type-safe: only these
// declared values are accepted, never an arbitrary string.
type capability string

func (c capability) String() string { return string(c) }

// Object-addressing capabilities: the set of TNS-key methods a compiled
// resource-object exposes (see pkg/specs/<res>). The engine only reads String();
// tcc-gen maps each to the specs method(s) it generates. Attached to the object
// template via engine.Addressing(...) on DefineIter.
const (
	HasBasicPath  capability = "basic"     // BasicPath(branch,commit,project,app,id)
	HasIndex      capability = "index"     // IndexValue(branch,project,app,id)
	HasIndexPath  capability = "indexPath" // IndexPath(project,app)
	HasHttp       capability = "http"      // HttpPath(fqdn)
	HasWasmModule capability = "wasm"      // WasmModulePath(...) + ModuleName(name)
	HasServices   capability = "services"  // ServicesPath(project,app,serviceId,command)
	HasWebSocket  capability = "websocket" // WebSocketPath / WebSocketHashPath (bespoke)
	HasNameIndex  capability = "nameIndex" // NameIndex(...) (bespoke)
	HasEmptyPath  capability = "empty"     // EmptyPath()
)

// compile-time check that the tags satisfy the engine interface.
var _ engine.Capability = HasBasicPath
