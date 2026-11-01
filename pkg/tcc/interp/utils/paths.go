package utils

import (
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
)

// The name->id index is keyed by the path of scopes a resource sits in, so building it
// means reading each scope's name — and which field that is comes from the schema
// (engine.Identity), carried down rather than assumed here.
func objectsPathToStringPath(opath []any, nameField string, rest ...string) (string, error) {
	ret := make([]string, 0, len(opath))
	for _, p := range opath {
		op, ok := p.(object.Object[object.Refrence])
		if !ok {
			return "", fmt.Errorf("path contains invalid type %T", p)
		}
		no := op.Get(nameField)
		if no == nil {
			return "", fmt.Errorf("path contains no name")
		}
		nameStr, ok := no.(string)
		if !ok {
			return "", fmt.Errorf("name is not a string")
		}
		ret = append(ret, nameStr)
	}
	return path.Join(append(ret, rest...)...), nil
}

func IndexById(ct transform.Context[object.Refrence], group, name, id, nameField string) error {
	ctp := ct.Path()
	if len(ctp) == 0 {
		return errors.New("context path is empty")
	}

	op, err := objectsPathToStringPath(ctp[1:], nameField, group, name)
	if err != nil {
		return err
	}

	_, err = ct.Store().String(op).Set(id)

	return err
}

func localResolveNameToId(store transform.Store[object.Refrence], ctp []any, group, name, nameField string) (string, error) {
	if len(ctp) == 0 {
		return "", errors.New("context path is empty")
	}

	op, err := objectsPathToStringPath(ctp[1:], nameField, group, name)
	if err != nil {
		return "", err
	}

	if !store.String(op).Exist() {
		return "", errors.New("not indexed")
	}

	return store.String(op).Get(), nil
}

// ResolveNameToId resolves a referenced name to its compiled id, honouring scope
// qualifiers (a name is a variable name, so they cannot collide):
//
//   - "name"      current scope, then ancestors up to root (default)
//   - "/name"     the root/global scope only (absolute)
//   - "../name"   the parent scope, exact (repeatable: "../../name")
func ResolveNameToId(ct transform.Context[object.Refrence], group, name, nameField string) (string, error) {
	ctp := ct.Path()
	if len(ctp) == 0 {
		return "", errors.New("context path is empty")
	}

	scope, bare, exact, err := scopeFor(ctp, name)
	if err != nil {
		return "", err
	}

	if exact {
		return localResolveNameToId(ct.Store(), scope, group, bare, nameField)
	}
	var ret string
	for _, at := range Outward(scope) {
		if ret, err = localResolveNameToId(ct.Store(), at, group, bare, nameField); err == nil {
			return ret, nil
		}
	}
	return "", err
}

// Outward is the chain a default reference resolves along: the scope itself, then
// each parent up to the root (app -> project -> root), matching the worker's order.
func Outward(scope []any) [][]any {
	out := make([][]any, 0, len(scope))
	for i := len(scope); i >= 1; i-- {
		out = append(out, scope[:i])
	}
	return out
}

// scopeFor strips leading scope qualifiers from name and returns the target scope
// path (a prefix of ctp), the bare name, and whether resolution is exact (no
// ancestor fallback). "/" targets root; each leading "../" climbs one level.
func scopeFor(ctp []any, name string) (scope []any, bare string, exact bool, err error) {
	if strings.HasPrefix(name, "/") {
		return ctp[:1], name[1:], true, nil // root/global, absolute
	}
	up := 0
	for strings.HasPrefix(name, "../") {
		up++
		name = strings.TrimPrefix(name, "../")
	}
	if up == 0 {
		return ctp, name, false, nil // default: current scope + ancestor fallback
	}
	if up >= len(ctp) {
		return nil, name, false, fmt.Errorf("reference scope %q climbs above the project root", name)
	}
	return ctp[:len(ctp)-up], name, true, nil // parent(s), exact
}

func ResolveNamesToId(ct transform.Context[object.Refrence], group string, names []string, nameField string) ([]string, error) {
	ctp := ct.Path()
	if len(ctp) == 0 {
		return nil, errors.New("context path is empty")
	}

	ret := make([]string, 0, len(names))
	for _, name := range names {
		r, err := ResolveNameToId(ct, group, name, nameField)
		if err != nil {
			return nil, err
		}

		ret = append(ret, r)
	}

	return ret, nil
}
