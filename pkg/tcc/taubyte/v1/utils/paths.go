package utils

import (
	"errors"
	"fmt"
	"path"

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

func ResolveNameToId(ct transform.Context[object.Refrence], group, name string) (string, error) {
	ctp := ct.Path()
	if len(ctp) == 0 {
		return "", errors.New("context path is empty")
	}

	ret, err := localResolveNameToId(ct.Store(), ctp, group, name)
	if err != nil {
		// try global
		return localResolveNameToId(ct.Store(), ctp[:1], group, name)
	}

	return ret, nil
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
