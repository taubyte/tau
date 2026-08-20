package schema

import (
	"fmt"

	"github.com/taubyte/tau/core/common/repositorytype"
	"github.com/taubyte/tau/pkg/specs/common"
	"github.com/taubyte/tau/pkg/specs/methods"
	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/interp"
)

// WHAT THIS VOCABULARY PUTS IN THE INDEX.
//
// The interpreter walks the indexed groups in order, works out each resource's identity,
// and asks — here — what that resource contributes. Every path below is a TNS path, and
// TNS is tau's: pkg/specs/methods, pkg/specs/common, core/common/repositorytype. All
// three used to be imported by the interpreter, which meant the generic layer carried one
// vocabulary's address space and a second vocabulary could not have an index at all
// without editing it.
//
// The five footprints are the ones v1 has always had, unchanged in what they write:
//
//	byName        a link bucket under the capability's by-name path
//	foreignKey    the domain-style fan-out: one link per resolved target
//	repository    the git-repo reverse index a website or library contributes
//	nameIndex     the id-keyed name entry
//	byScope       a per-(project, scope) aggregate every instance appends to
//	placeholder   a key reserved for an fqdn nothing links to yet

// indexValue is the value every link bucket accumulates: this resource's TNS path.
func indexValue(ic interp.IndexCtx) (*common.TnsPath, error) {
	return methods.IndexValue(ic.Branch, ic.Project, ic.Scope, ic.ID, common.PathVariable(ic.Kind))
}

// byName links this resource under the capability's by-name path.
func byName(cap *Cap) func(interp.IndexCtx) ([]interp.IndexEntry, error) {
	return func(ic interp.IndexCtx) ([]interp.IndexEntry, error) {
		if cap.ByName == nil {
			return nil, fmt.Errorf("index by name: capability %q carries no by-name path", cap.Name)
		}
		p, err := cap.ByName(ic.Project, ic.Scope, ic.Name, common.PathVariable(ic.Kind))
		if err != nil {
			return nil, fmt.Errorf("index by name for %s %s failed with %w", ic.Kind, ic.ID, err)
		}
		v, err := indexValue(ic)
		if err != nil {
			return nil, err
		}
		return []interp.IndexEntry{{Path: p.Versioning().Links().String(), Value: v.String(), Append: true}}, nil
	}
}

// foreignKey is the domain-style fan-out: for each resolved id in refField, read the
// target's key and link this resource under the capability's path for that value.
func foreignKey(cap *Cap, refField, targetGroup, targetKey string) func(interp.IndexCtx) ([]interp.IndexEntry, error) {
	return func(ic interp.IndexCtx) ([]interp.IndexEntry, error) {
		if cap.ForeignKey == nil {
			return nil, fmt.Errorf("index foreign key: capability %q carries no foreign-key path", cap.Name)
		}
		refVal := ic.Obj.Get(refField)
		refs, ok := refVal.([]string)
		if !ok && refVal != nil {
			return nil, fmt.Errorf("%s is not a []string", refField)
		}
		if len(refs) == 0 {
			return nil, nil
		}
		v, err := indexValue(ic)
		if err != nil {
			return nil, err
		}

		out := make([]interp.IndexEntry, 0, len(refs))
		for _, targetID := range refs {
			targetObj, ok := ic.Lookup(targetGroup, targetID)
			if !ok {
				return nil, fmt.Errorf("fetching %s object for %s failed", targetGroup, targetID)
			}
			keyVal, err := targetObj.GetString(targetKey)
			if err != nil {
				return nil, fmt.Errorf("%s is not a string for %s %s: %w", targetKey, targetGroup, targetID, err)
			}
			p, err := cap.ForeignKey(keyVal, common.PathVariable(ic.Kind))
			if err != nil {
				return nil, fmt.Errorf("getting %s path for %s %s failed with %w", cap.Name, targetGroup, targetID, err)
			}
			out = append(out, interp.IndexEntry{Path: p.Versioning().Links().String(), Value: v.String(), Append: true})
		}
		return out, nil
	}
}

// repository is the git-repo reverse index: the repo's type, and this resource under it.
func repository(repoType repositorytype.Type, providerField, idField string) func(interp.IndexCtx) ([]interp.IndexEntry, error) {
	return func(ic interp.IndexCtx) ([]interp.IndexEntry, error) {
		provider, err := ic.Obj.GetString(providerField)
		if err != nil {
			return nil, fmt.Errorf("%s is not a string: %w", providerField, err)
		}
		repoID, err := ic.Obj.GetString(idField)
		if err != nil {
			return nil, fmt.Errorf("%s is not a string: %w", idField, err)
		}
		rp, err := methods.GetRepositoryPath(provider, repoID, ic.Project)
		if err != nil {
			return nil, fmt.Errorf("getting repository path for %s failed with %w", repoID, err)
		}
		v, err := indexValue(ic)
		if err != nil {
			return nil, err
		}
		return []interp.IndexEntry{
			{Path: rp.Type().String(), Value: repoType},
			{Path: rp.Resource(ic.ID).String(), Value: v.String()},
		}, nil
	}
}

// nameIndex is the id-keyed name entry: what this resource is called, by its id.
func nameIndex(ic interp.IndexCtx) ([]interp.IndexEntry, error) {
	return []interp.IndexEntry{
		{Path: methods.NameIndex(ic.ID, common.PathVariable(ic.Kind)).String(), Value: ic.Name},
	}, nil
}

// byScope is the per-(project, scope) aggregate: every instance appends to one key, with
// no Links() suffix, so the scope has a single list of everything in it.
func byScope(cap *Cap) func(interp.IndexCtx) ([]interp.IndexEntry, error) {
	return func(ic interp.IndexCtx) ([]interp.IndexEntry, error) {
		if cap.ScopePath == nil {
			return nil, fmt.Errorf("index by scope: capability %q carries no scope path", cap.Name)
		}
		p, err := cap.ScopePath(ic.Project, ic.Scope)
		if err != nil {
			return nil, fmt.Errorf("getting scope path for %q failed with %w", cap.Name, err)
		}
		v, err := indexValue(ic)
		if err != nil {
			return nil, err
		}
		return []interp.IndexEntry{{Path: p.String(), Value: v.String(), Append: true}}, nil
	}
}

// placeholder reserves the key an fqdn resolves to, without claiming anything is at it.
func placeholder(keyField string) func(interp.IndexCtx) ([]interp.IndexEntry, error) {
	return func(ic interp.IndexCtx) ([]interp.IndexEntry, error) {
		fqdn, err := ic.Obj.GetString(keyField)
		if err != nil {
			return nil, fmt.Errorf("index key %s is not a string: %w", keyField, err)
		}
		p, err := methods.ReversedFqdnBasicPath(fqdn, common.PathVariable(ic.Kind))
		if err != nil {
			return nil, fmt.Errorf("getting basic path for the index key failed with %w", err)
		}
		return []interp.IndexEntry{{Path: p.Versioning().Links().String(), Value: nil}}, nil
	}
}

// indexes runs several footprints in order and concatenates what they contribute. The
// ORDER within a resource matters for the same reason the group order does: a bucket
// accumulates.
func indexes(fns ...func(interp.IndexCtx) ([]interp.IndexEntry, error)) interp.Indexer {
	return func(ic interp.IndexCtx) ([]interp.IndexEntry, error) {
		var out []interp.IndexEntry
		for _, fn := range fns {
			entries, err := fn(ic)
			if err != nil {
				return nil, err
			}
			out = append(out, entries...)
		}
		return out, nil
	}
}

// Cap is a rich object-addressing capability: it names itself (satisfying
// engine.Capability, the only thing the engine requires) and carries the role-typed TNS
// path functions this vocabulary's index footprints call.
//
// It used to live in interp, whose own comment admitted why it could not: its funcs
// reference pkg/specs, which is tau's address space and not the interpreter's.
type Cap struct {
	Name string

	// Gen is the object-addressing methods this capability contributes to a generated
	// structureSpec struct — the codegen face tcc-gen renders through the
	// engine.MethodCarrier interface. Empty for a capability with no methods.
	Gen []engine.MethodSpec

	// ByName computes the by-name index path from the group's PathVariable; ForeignKey
	// the path for a resolved target value; ScopePath the per-(project, scope) aggregate.
	// Each is nil unless the capability plays that role.
	ByName     func(project, app, name string, group common.PathVariable) (*common.TnsPath, error)
	ForeignKey func(value string, group common.PathVariable) (*common.TnsPath, error)
	ScopePath  func(project, app string) (*common.TnsPath, error)
}

// String makes Cap an engine.Capability: the term's identity, e.g. "wasm".
func (c *Cap) String() string { return c.Name }

// AddressingMethods makes Cap an engine.MethodCarrier, handing the generator the method
// specs declared on the term so tcc-gen renders them without a switch.
func (c *Cap) AddressingMethods() []engine.MethodSpec { return c.Gen }

var _ engine.MethodCarrier = (*Cap)(nil)
