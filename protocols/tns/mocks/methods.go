package mocks

import (
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/taubyte/go-interfaces/services/tns"
	"github.com/taubyte/go-specs/common"
	"github.com/taubyte/go-specs/methods"
	structureSpec "github.com/taubyte/go-specs/structure"
)

func (m *mockTns) Push(_path []string, data interface{}) error {
	if len(_path) == 0 {
		return errors.New("path is nil")
	}

	m.lock.Lock()
	m.mapDef[path.Join(_path...)] = data
	m.lock.Unlock()

	return nil
}

func (m *mockTns) Fetch(_path tns.Path) (tns.Object, error) {
	m.lock.RLock()
	value, exists := m.mapDef[_path.String()]
	m.lock.RUnlock()
	if !exists {
		return nil, fmt.Errorf("no value stored in path `%s`", _path.String())
	}

	return &mockedObject{
		path:  _path,
		value: value,
	}, nil
}

func (m *mockedObject) Path() tns.Path {
	return m.path
}

func (m *mockedObject) Interface() interface{} {
	return m.value
}

func (m *mockedObject) Current(branch string) ([]tns.Path, error) {
	if len(branch) < 1 {
		return nil, errors.New("unknown branch")
	}

	currentPaths, ok := m.value.([]string)
	if !ok {
		return nil, fmt.Errorf("invalid type for mocked object, `%T` expected `[]string`", m.value)
	}

	tnsPaths := []tns.Path{}
	for _, _path := range currentPaths {
		tnsPaths = append(tnsPaths, &mockedPath{path: _path})
	}

	return tnsPaths, nil
}

func (m *mockedPath) String() string {
	return m.path
}

func (m *mockedPath) Slice() []string {
	return strings.Split(m.path, "/")
}

// TODO: Need a cleaner implementation, generics make this tough
// TODO: Incomplete currently only compatible for VM test case, need to compare against config compiler
func (m *mockTns) Inject(structure interface{}, config InjectConfig) error {
	toPush := []InjectObj{}
	if len(config.Commit) == 0 {
		config.Commit = "head_commit"
	}
	if len(config.Branch) == 0 {
		config.Branch = common.DefaultBranch
	}
	if len(config.Project) == 0 {
		config.Project = "test_project"
	}

	if basicCast, ok := structure.(structureSpec.Basic); ok {
		basicPath, err := basicCast.BasicPath(config.Branch, config.Commit, config.Project, config.Application)
		if err != nil {
			return err
		}

		toPush = append(toPush, InjectObj{Path: basicPath})

		if wasmCast, ok := structure.(structureSpec.Wasm); ok {
			if len(config.Cid) == 0 {
				return errors.New("asset cid is required for wasm structure")
			}

			wasmPath, err := wasmCast.WasmModulePath(config.Project, config.Application)
			if err != nil {
				return err
			}

			assetPath, err := methods.GetTNSAssetPath(config.Project, wasmCast.GetId(), config.Branch)
			if err != nil {
				return err
			}

			toPush = append(toPush,
				InjectObj{
					Path:  wasmPath,
					Value: []string{basicPath.String()},
				},
				InjectObj{
					Path:  assetPath,
					Value: config.Cid,
				},
			)
		}
	} else {
		return fmt.Errorf("type %T is not supported", structure)
	}

	for _, obj := range toPush {
		if err := m.Push(obj.Path.Slice(), obj.Value); err != nil {
			return err
		}
	}

	return nil
}

func (m *mockTns) Delete(path tns.Path) {
	m.lock.Lock()
	delete(m.mapDef, path.String())
	m.lock.Unlock()
}
