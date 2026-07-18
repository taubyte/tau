package driver

import (
	"github.com/taubyte/tau/pkg/specs/common"
	"github.com/taubyte/tau/pkg/tcc/engine"
)

// Cap is a rich object-addressing capability: it names itself (satisfying
// engine.Capability, the only thing the engine requires) and carries the
// role-typed TNS-path functions the index driver needs, so the capability's
// runtime meaning lives on the term instead of a term-keyed switch. Each role is
// nil unless the capability plays it: ByName for a by-Name index link (wasm ->
// WasmModulePath, indexPath -> IndexPath), ForeignKey for the domain-style
// fan-out link (http -> HttpPath), ScopePath for a per-(project,app) aggregate
// (websocket -> WebSocketHashPath). It lives in driver because its funcs reference
// pkg/specs; schema binds them (schema already imports driver + specs).
type Cap struct {
	Name string

	// Gen is the object-addressing methods this capability contributes to a
	// generated structureSpec struct — the codegen face tcc-gen renders through
	// the engine.MethodCarrier interface. Empty for a capability with no methods.
	Gen []engine.MethodSpec

	// ByName computes the capability's by-Name index path from the group's
	// PathVariable — used by IndexByName. nil unless the capability is a by-Name
	// index role.
	ByName func(project, app, name string, group common.PathVariable) (*common.TnsPath, error)
	// ForeignKey computes the capability's path from a resolved target value —
	// used by IndexForeignKey. nil unless the capability is a foreign-key role.
	ForeignKey func(value string, group common.PathVariable) (*common.TnsPath, error)
	// ScopePath computes the capability's per-(project,app) scope path — used by
	// IndexByScope. nil unless the capability is a scope role.
	ScopePath func(project, app string) (*common.TnsPath, error)
}

// String makes Cap an engine.Capability. It is the term's identity (e.g. "wasm")
// — the same string the old typed capability alias carried, so a generator that
// reads only String() is unaffected.
func (c *Cap) String() string { return c.Name }

// AddressingMethods makes Cap an engine.MethodCarrier: it hands the generator the
// method specs declared on the term, so tcc-gen renders them without a switch.
func (c *Cap) AddressingMethods() []engine.MethodSpec { return c.Gen }

var _ engine.MethodCarrier = (*Cap)(nil)
