package session

import (
	"context"
	"fmt"
	"strings"

	"github.com/taubyte/tau/pkg/tcc/interp"
)

// WHAT A RESOURCE BECAME.
//
// Some kinds are rewritten when they compile: a workflow is not run by anything, and what
// runs is the queues and functions it becomes. That is not a second operation a caller
// asks for — compiling IS lowering — so the question here is not "lower this", it is
// "which of these compiled resources exist because I wrote that one".
//
// The compiled object answers it: the compile records what it produced and where each
// piece came from. A console opening a workflow, and a CLI showing what a deploy will
// contain, are both reading that record.

// Became is what one resource became: the compiled object, the ids of the resources
// produced from this one, and the access entries its lowering added to resources
// somebody else wrote.
//
// A resource that becomes nothing — most of them — answers with empty lists and no
// error. Asking is not a mistake.
func (s *Session) Became(ctx context.Context, res []string, opts CompileOptions) (interp.Object, []string, []interp.Granted, error) {
	kind, name, scope, err := s.resourceAt(res)
	if err != nil {
		return nil, nil, nil, err
	}

	obj, _, err := s.Compile(ctx, opts)
	if err != nil {
		return nil, nil, nil, err
	}

	id, found := idOf(obj.Flat(), kind, name, scope)
	if !found {
		return obj, nil, nil, fmt.Errorf("session: %s/%s is not in the compiled configuration", kind, name)
	}
	made, added := interp.LoweredOf(obj.Flat()).Of(id)
	return obj, made, added, nil
}

// idOf finds a resource's compiled id by what it is called and where it lives.
func idOf(flat map[string]any, kind, name, scope string) (string, bool) {
	root, ok := flat["object"].(map[string]any)
	if !ok {
		root = flat
	}
	for _, r := range interp.LoweredOf(flat).Resources {
		if r.Kind == kind && r.Name == name && r.Scope == scope {
			// a resource the compile produced is found the same way, and its id is
			// already the key of the record
			break
		}
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
		if n, _ := res["name"].(string); n == name {
			return id, true
		}
	}
	return "", false
}

// resourceAt reads an ordinary resource address — the same []string Get, Set and
// ValidateResource take — as the kind, name and scope the compiled object knows it by.
//
// Scopes NEST, so the address does too: [namespace, shop, namespace, eu, function,
// notify] is `notify` in `shop/eu`. Reading only the two shapes that fit in four
// segments would have answered "not a resource" for anything two namespaces deep, which
// is a limit on the vocabulary invented by the reader.
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
