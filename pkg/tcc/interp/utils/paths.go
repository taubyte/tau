package utils

import (
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
)

func objectsPathToStringPath(opath []any, rest ...string) (string, error) {
	ret := make([]string, 0, len(opath))
	for _, p := range opath {
		op, ok := p.(object.Object[object.Refrence])
		if !ok {
			return "", fmt.Errorf("path contains invalid type %T", p)
		}
		no := op.Get("name")
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

func IndexById(ct transform.Context[object.Refrence], group, name, id string) error {
	ctp := ct.Path()
	if len(ctp) == 0 {
		return errors.New("context path is empty")
	}

	op, err := objectsPathToStringPath(ctp[1:], group, name)
	if err != nil {
		return err
	}

	_, err = ct.Store().String(op).Set(id)

	return err
}

func localResolveNameToId(store transform.Store[object.Refrence], ctp []any, group, name string) (string, error) {
	if len(ctp) == 0 {
		return "", errors.New("context path is empty")
	}

	op, err := objectsPathToStringPath(ctp[1:], group, name)
	if err != nil {
		return "", err
	}

	if !store.String(op).Exist() {
		return "", errors.New("not indexed")
	}

	return store.String(op).Get(), nil
}

// ResolveNameToId resolves a referenced name to its compiled id, honoring optional
// scope qualifiers on the name. The scoping is generic (every Ref benefits) and
// filesystem-like — a name is never a bare "/" or ".." (names are variable-names),
// so the qualifiers can't collide:
//
//   - "name"      current scope, then fall back to ancestors up to root (default)
//   - "/name"     the root/global scope only (absolute)
//   - "../name"   the parent scope, exact (repeatable: "../../name")
//
// The default arm is byte-for-byte the previous behavior, so existing configs
// (and the parity oracle) are unaffected; the qualifiers are pure additions.
func ResolveNameToId(ct transform.Context[object.Refrence], group, name string) (string, error) {
	ctp := ct.Path()
	if len(ctp) == 0 {
		return "", errors.New("context path is empty")
	}

	scope, bare, exact, err := scopeFor(ctp, name)
	if err != nil {
		return "", err
	}

	ret, err := localResolveNameToId(ct.Store(), scope, group, bare)
	if err == nil || exact {
		return ret, err
	}

	// Default arm only: fall back to parent scopes (app -> project -> root),
	// matching substrate resolution order.
	for i := len(scope) - 1; i >= 1; i-- {
		if ret, err = localResolveNameToId(ct.Store(), scope[:i], group, bare); err == nil {
			return ret, nil
		}
	}

	return "", err
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

func ResolveNamesToId(ct transform.Context[object.Refrence], group string, names []string) ([]string, error) {
	ctp := ct.Path()
	if len(ctp) == 0 {
		return nil, errors.New("context path is empty")
	}

	ret := make([]string, 0, len(names))
	for _, name := range names {
		r, err := ResolveNameToId(ct, group, name)
		if err != nil {
			return nil, err
		}

		ret = append(ret, r)
	}

	return ret, nil
}
