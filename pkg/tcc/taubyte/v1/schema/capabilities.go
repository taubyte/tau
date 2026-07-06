package schema

import "github.com/taubyte/tau/pkg/tcc/engine"

// Object-addressing capabilities: the set of TNS-key methods a compiled
// resource-object exposes (see pkg/specs/<res>). The values are opaque strings to
// the engine; tcc-gen maps each to the specs method(s) it generates. Attached to
// the object template via engine.Addressing(...) on DefineIter.
const (
	HasBasicPath  engine.Capability = "basic"     // BasicPath(branch,commit,project,app,id)
	HasIndex      engine.Capability = "index"     // IndexValue(branch,project,app,id)
	HasIndexPath  engine.Capability = "indexPath" // IndexPath(project,app)
	HasHttp       engine.Capability = "http"      // HttpPath(fqdn)
	HasWasmModule engine.Capability = "wasm"      // WasmModulePath(...) + ModuleName(name)
	HasServices   engine.Capability = "services"  // ServicesPath(project,app,serviceId,command)
	HasWebSocket  engine.Capability = "websocket" // WebSocketPath / WebSocketHashPath (bespoke)
	HasNameIndex  engine.Capability = "nameIndex" // NameIndex(...) (bespoke)
	HasEmptyPath  engine.Capability = "empty"     // EmptyPath()
)
