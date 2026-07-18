package schema

import (
	"github.com/taubyte/tau/pkg/specs/common"
	"github.com/taubyte/tau/pkg/specs/methods"
	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v1/driver"
)

// Object-addressing capabilities: the set of TNS-key methods a compiled
// resource-object exposes (see pkg/specs/<res>). Each is a *driver.Cap carrying
// its own meaning as data — the runtime path funcs the index driver reads
// (ByName/ForeignKey/ScopePath, bound directly to the matching
// pkg/specs/methods helper) AND the Gen method specs tcc-gen renders into the
// structureSpec struct. So both the driver and the generator read a term's
// behaviour off the term instead of a term-keyed switch. Attached to the object
// template via engine.Addressing(...) on DefineIter.
//
// The engine only requires String(); the generator only requires
// AddressingMethods(). In a Gen method spec "@" expands to the method receiver
// and ViaTns picks the delegate form (<alias>.Tns().<Name> vs <alias>.<Name>).
var (
	// BasicPath(branch,commit,project,app,id)
	HasBasicPath = &driver.Cap{
		Name: "basic",
		Gen: []engine.MethodSpec{
			{Name: "BasicPath", Params: "branch, commit, project, app string", Ret: "(*common.TnsPath, error)", Args: "branch, commit, project, app, @.Id", ViaTns: true},
		},
	}
	// IndexValue(branch,project,app,id)
	HasIndex = &driver.Cap{
		Name: "index",
		Gen: []engine.MethodSpec{
			{Name: "IndexValue", Params: "branch, project, app string", Ret: "(*common.TnsPath, error)", Args: "branch, project, app, @.Id", ViaTns: true},
		},
	}
	// IndexPath(project,app)
	HasIndexPath = &driver.Cap{
		Name: "indexPath",
		Gen: []engine.MethodSpec{
			{Name: "IndexPath", Params: "project, app string", Ret: "*common.TnsPath", Args: "project, app, @.Name", ViaTns: true},
		},
		ByName: func(project, app, name string, _ common.PathVariable) (*common.TnsPath, error) {
			return methods.IndexPath(project, app, name), nil
		},
	}
	// HttpPath(fqdn)
	HasHttp = &driver.Cap{
		Name: "http",
		Gen: []engine.MethodSpec{
			{Name: "HttpPath", Params: "fqdn string", Ret: "(*common.TnsPath, error)", Args: "fqdn", ViaTns: true},
		},
		ForeignKey: methods.HttpPath,
	}
	// WasmModulePath(...) + ModuleName(name)
	HasWasmModule = &driver.Cap{
		Name: "wasm",
		Gen: []engine.MethodSpec{
			{Name: "WasmModulePath", Params: "project, app string", Ret: "(*common.TnsPath, error)", Args: "project, app, @.Name", ViaTns: true},
			{Name: "ModuleName", Params: "", Ret: "string", Args: "@.Name", ViaTns: false},
		},
		ByName: methods.WasmModulePath,
	}
	// ServicesPath(project,app,serviceId,command)
	HasServices = &driver.Cap{
		Name: "services",
		Gen: []engine.MethodSpec{
			{Name: "ServicesPath", Params: "project, app, serviceId string", Ret: "(*common.TnsPath, error)", Args: "project, app, serviceId, @.Command", ViaTns: true},
		},
	}
	// WebSocketHashPath + WebSocketPath
	HasWebSocket = &driver.Cap{
		Name: "websocket",
		Gen: []engine.MethodSpec{
			{Name: "WebSocketHashPath", Params: "project, app string", Ret: "(*common.TnsPath, error)", Args: "project, app", ViaTns: true},
			{Name: "WebSocketPath", Params: "hash string", Ret: "(*common.TnsPath, error)", Args: "hash", ViaTns: true},
		},
		ScopePath: methods.WebSocketHashPath,
	}
	// NameIndex(name)
	HasNameIndex = &driver.Cap{
		Name: "nameIndex",
		Gen: []engine.MethodSpec{
			{Name: "NameIndex", Params: "", Ret: "*common.TnsPath", Args: "@.Name", ViaTns: true},
		},
	}
	// EmptyPath()
	HasEmptyPath = &driver.Cap{
		Name: "empty",
		Gen: []engine.MethodSpec{
			{Name: "EmptyPath", Params: "branch, commit, project, app string", Ret: "(*common.TnsPath, error)", Args: "branch, commit, project, app", ViaTns: true},
		},
	}
)

// compile-time check that the tags satisfy the engine interfaces.
var (
	_ engine.Capability    = HasBasicPath
	_ engine.MethodCarrier = HasBasicPath
)
