package session

import (
	"context"
	"fmt"
	"strings"

	"github.com/taubyte/tau/pkg/tcc/interp"
)

// What a resource became: compiling is lowering, so the question is which
// compiled resources exist because one was written, answered from the record the
// compile keeps.

// Became is what one resource became: the compiled object, the ids produced from
// it, and the entries its lowering added elsewhere. Becoming nothing is not an
// error.
func (s *Session) Became(ctx context.Context, res []string, opts CompileOptions) (interp.Object, []string, []interp.Granted, error) {
	kind, name, scope, err := s.resourceAt(res)
	if err != nil {
		return nil, nil, nil, err
	}

	obj, _, err := s.Compile(ctx, opts)
	if err != nil {
		return nil, nil, nil, err
	}

	_, nameField := s.identity()
	id, found := idOf(obj.Flat(), kind, name, scope, nameField)
	if !found {
		return obj, nil, nil, fmt.Errorf("session: %s/%s is not in the compiled configuration", kind, name)
	}
	made, added := interp.LoweredOf(obj.Flat()).Of(id)
	return obj, made, added, nil
}

// idOf finds a resource's compiled id by what it is called and where it lives. What the
// name is IN is the vocabulary's answer, which the binding supplies — the session has no
// business guessing a field name any more than the interpreter does.
func idOf(flat map[string]any, kind, name, scope, nameField string) (string, bool) {
	root, ok := flat[interp.ProjectKey].(map[string]any)
	if !ok {
		root = flat
	}
	instances, ok := root[kind].(map[string]any)
	if !ok {
		return "", false
	}
	for id, v := range instances {
		res, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if n, _ := res[nameField].(string); n == name {
			return id, true
		}
	}
	return "", false
}

// resourceAt reads a resource address as kind, name and scope. Scopes nest:
// [namespace, shop, namespace, local, function, notify] is `notify` in
// `shop/local`.
func (s *Session) resourceAt(res []string) (kind, name, scope string, err error) {
	bad := func(why string) error {
		return fmt.Errorf("session: %q is not a resource address (%s)", strings.Join(res, "/"), why)
	}

	rest := res
	var scopes []string
	if c, ok := s.container(); ok {
		for len(rest) >= 2 && rest[0] == c.Group {
			scopes = append(scopes, rest[1])
			rest = rest[2:]
		}
	}
	if len(rest) != 2 {
		return "", "", "", bad("a resource is a kind and a name, inside as many scopes as it lives in")
	}
	k, err := s.kind(rest[0])
	if err != nil {
		return "", "", "", err
	}
	return k.Group, rest[1], strings.Join(scopes, "/"), nil
}

// identity is what this DSL calls a resource's id and its name. A binding that does not
// say leaves both empty, and a session over it can find nothing by name — which is the
// honest outcome rather than a guess that works for one vocabulary.
func (s *Session) identity() (id, name string) {
	if l, ok := s.bind.Layout.(IdentifiedLayout); ok {
		return l.Identity()
	}
	return "", ""
}
