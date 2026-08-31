package schema

import (
	"fmt"

	"github.com/taubyte/tau/core/common/repositorytype"
	"github.com/taubyte/tau/legacy/specs/common"
	"github.com/taubyte/tau/legacy/specs/methods"
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

// mine is the value every bucket accumulates: this resource's own TNS path. Five of the
// six footprints below link something to it, and the sixth reserves a key for one.
func mine(ic interp.IndexCtx) (string, error) {
	p, err := methods.IndexValue(ic.Branch, ic.Project, ic.Scope.IDs(), ic.ID, common.PathVariable(ic.Kind))
	if err != nil {
		return "", err
	}
	return p.String(), nil
}

// linkTo is the shape most of this index is: work out a path, and put this resource in
// the bucket there. byName, byScope and the fan-out are all this, with a different path.
func linkTo(path func(interp.IndexCtx) (string, error)) interp.Indexer {
	return func(ic interp.IndexCtx) ([]interp.IndexEntry, error) {
		key, err := path(ic)
		if err != nil {
			return nil, err
		}
		value, err := mine(ic)
		if err != nil {
			return nil, err
		}
		return []interp.IndexEntry{interp.Link(key, value)}, nil
	}
}

// byName: the bucket under the capability's by-name path, which is how a resource is
// found by what it is called.
func byName(cap *Cap) interp.Indexer {
	return linkTo(func(ic interp.IndexCtx) (string, error) {
		if cap.ByName == nil {
			return "", fmt.Errorf("capability %q carries no by-name path", cap.Name)
		}
		p, err := cap.ByName(ic.Project, ic.Scope.IDs(), ic.Name, common.PathVariable(ic.Kind))
		if err != nil {
			return "", err
		}
		return p.Versioning().Links().String(), nil
	})
}

// byScope: one key per (project, scope) that every instance appends to, with no Links()
// suffix — the scope's single list of everything in it.
func byScope(cap *Cap) interp.Indexer {
	return linkTo(func(ic interp.IndexCtx) (string, error) {
		if cap.ScopePath == nil {
			return "", fmt.Errorf("capability %q carries no scope path", cap.Name)
		}
		p, err := cap.ScopePath(ic.Project, ic.Scope.IDs())
		if err != nil {
			return "", err
		}
		return p.String(), nil
	})
}

// foreignKey: the domain-style fan-out. For every resource this one references, read what
// that one is called and link this one under the capability's path for that name.
func foreignKey(cap *Cap, refField, targetGroup, targetKey string) interp.Indexer {
	return func(ic interp.IndexCtx) ([]interp.IndexEntry, error) {
		if cap.ForeignKey == nil {
			return nil, fmt.Errorf("capability %q carries no foreign-key path", cap.Name)
		}
		values, err := ic.Each(refField, targetGroup, targetKey)
		if err != nil || len(values) == 0 {
			return nil, err
		}
		value, err := mine(ic)
		if err != nil {
			return nil, err
		}

		out := make([]interp.IndexEntry, 0, len(values))
		for _, v := range values {
			p, err := cap.ForeignKey(v, common.PathVariable(ic.Kind))
			if err != nil {
				return nil, fmt.Errorf("getting the %s path for %q failed with %w", cap.Name, v, err)
			}
			out = append(out, interp.Link(p.Versioning().Links().String(), value))
		}
		return out, nil
	}
}

// repository: the git-repo reverse index — what kind of repository it is, and this
// resource under it.
func repository(repoType repositorytype.Type, providerField, idField string) interp.Indexer {
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
			return nil, fmt.Errorf("getting the repository path for %s failed with %w", repoID, err)
		}
		value, err := mine(ic)
		if err != nil {
			return nil, err
		}
		return []interp.IndexEntry{
			interp.Set(rp.Type().String(), repoType),
			interp.Set(rp.Resource(ic.ID).String(), value),
		}, nil
	}
}

// nameIndex: what this resource is called, found by its id.
func nameIndex(ic interp.IndexCtx) ([]interp.IndexEntry, error) {
	return []interp.IndexEntry{
		interp.Set(methods.NameIndex(ic.ID, common.PathVariable(ic.Kind)).String(), ic.Name),
	}, nil
}

// placeholder: reserve the key an fqdn resolves to. A name nothing links to yet is not
// the same as a name nobody has mentioned, and the difference is what a lookup returns.
func placeholder(keyField string) interp.Indexer {
	return func(ic interp.IndexCtx) ([]interp.IndexEntry, error) {
		fqdn, err := ic.Obj.GetString(keyField)
		if err != nil {
			return nil, fmt.Errorf("%s is not a string: %w", keyField, err)
		}
		p, err := methods.ReversedFqdnBasicPath(fqdn, common.PathVariable(ic.Kind))
		if err != nil {
			return nil, fmt.Errorf("getting the basic path for %q failed with %w", fqdn, err)
		}
		return []interp.IndexEntry{interp.Hole(p.Versioning().Links().String())}, nil
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
